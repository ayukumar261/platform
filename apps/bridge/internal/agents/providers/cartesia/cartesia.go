package cartesia

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"bridge/internal/agents"

	"github.com/gorilla/websocket"
)

const (
	endpoint     = "wss://api.cartesia.ai/v1/agents/websocket/"
	apiVersion   = "2026-08-14"
	writeTimeout = 5 * time.Second
)

// Input Formats
var inputFormats = map[agents.AudioFormat]string{
	{Encoding: agents.PCM16LE, SampleRate: 16000, Channels: 1}: "pcm_16000",
	{Encoding: agents.PCM16LE, SampleRate: 24000, Channels: 1}: "pcm_24000",
	{Encoding: agents.PCM16LE, SampleRate: 44100, Channels: 1}: "pcm_44100",
	{Encoding: agents.MuLaw, SampleRate: 8000, Channels: 1}:    "mulaw_8000",
}

type Provider struct {
	apiKey   string
	endpoint string
}

// New creates a provider without contacting Cartesia.
func New(apiKey string) *Provider {
	return &Provider{apiKey: apiKey, endpoint: endpoint}
}

// Name returns "cartesia".
func (*Provider) Name() string {
	return "cartesia"
}

// NewSession prepares a session with a saved agent. It connects on Start.
//
// Parameters:
//   - config: the agent's ID and the audio format for both directions
//
// Returns:
//   - the session, ready to start
//   - an error if Cartesia does not support the audio format
func (p *Provider) NewSession(_ context.Context, config agents.SessionConfig) (agents.Session, error) {
	format, ok := inputFormats[config.Audio]
	if !ok {
		return nil, fmt.Errorf("cartesia does not support audio format %+v", config.Audio)
	}

	return &session{
		url:    p.endpoint + url.PathEscape(config.AgentID) + "?cartesia_version=" + apiVersion,
		apiKey: p.apiKey,
		format: format,
		events: make(chan agents.Event, 16),
		closed: make(chan struct{}),
	}, nil
}

type session struct {
	url    string
	apiKey string
	format string

	conn      *websocket.Conn
	writeMu   sync.Mutex
	events    chan agents.Event
	closed    chan struct{}
	closeOnce sync.Once
}

// Start connects to the agent and waits until it can listen.
//
// The steps are:
//  1. Open the WebSocket with the API key.
//  2. Send session_create with the audio format.
//  3. Wait for session_ready, then read events in the background.
//
// Parameters:
//   - ctx: stops the startup early if it is cancelled or times out
//
// Returns:
//   - nil once the agent can listen
//   - an error if Cartesia cannot be reached or rejects the session
func (s *session) Start(ctx context.Context) error {
	// Open the WebSocket with the API key
	conn, response, err := websocket.DefaultDialer.DialContext(ctx, s.url, http.Header{"X-API-Key": {s.apiKey}})
	if err != nil {
		// Report a rejected connection's status, such as 401
		if response != nil {
			return fmt.Errorf("connect to Cartesia: %s", response.Status)
		}
		return fmt.Errorf("connect to Cartesia: %w", err)
	}
	s.conn = conn

	// Create the session, giving up if ctx ends first
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	err = s.handshake()
	if !stop() {
		return ctx.Err()
	}
	if err != nil {
		return err
	}

	// Read events until the session ends
	go s.read()
	return nil
}

// handshake sends session_create and waits for session_ready.
//
// Returns:
//   - nil once the agent can accept audio
//   - an error if the write fails or Cartesia rejects the session
func (s *session) handshake() error {
	// Tell Cartesia the audio format
	err := s.send(map[string]any{
		"type":  "session_create",
		"audio": map[string]any{"input_format": s.format},
	})
	if err != nil {
		return err
	}

	// Wait until the agent can accept audio
	var reply struct {
		Type string `json:"type"`
		Code string `json:"code"`
	}
	if err := s.conn.ReadJSON(&reply); err != nil {
		return fmt.Errorf("wait for session_ready: %w", err)
	}
	if reply.Type != "session_ready" {
		return fmt.Errorf("expected session_ready, got %+v", reply)
	}
	return nil
}

// SendAudio sends caller audio in the session's format.
func (s *session) SendAudio(_ context.Context, audio []byte) error {
	// Send the audio, which encoding/json writes as base64
	return s.send(map[string]any{"type": "audio_input", "audio": audio})
}

// send writes one message to Cartesia. A failed write closes the connection,
// so that the reader reports the failure through Events.
//
// Parameters:
//   - message: the message, which is written as JSON
//
// Returns:
//   - nil once the message is written
//   - an error if the write fails
func (s *session) send(message any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := s.conn.WriteJSON(message); err != nil {
		s.conn.Close()
		return fmt.Errorf("write to Cartesia: %w", err)
	}
	return nil
}

// Events delivers the agent's events after Start succeeds.
func (s *session) Events() <-chan agents.Event {
	return s.events
}

type serverMessage struct {
	Type            string         `json:"type"`
	Audio           []byte         `json:"audio"`
	Digit           string         `json:"digit"`
	Role            agents.Speaker `json:"role"`
	Text            string         `json:"text"`
	ToolCallID      string         `json:"tool_call_id"`
	ExpectsResponse bool           `json:"expects_response"`
}

// read turns Cartesia's messages into events until the connection closes.
func (s *session) read() {
	defer close(s.events)

	for {
		// Wait for the next message, or report why the connection closed
		var message serverMessage
		if err := s.conn.ReadJSON(&message); err != nil {
			s.emit(closeEvent(err))
			return
		}

		// Translate the message into an event
		var event agents.Event
		switch message.Type {
		case "audio_output":
			event = agents.Event{Type: agents.AudioOutput, Audio: message.Audio}
		case "audio_output_clear":
			event = agents.Event{Type: agents.ClearAudio}
		case "dtmf_output":
			event = agents.Event{Type: agents.SendDTMF, Digits: message.Digit}
		case "turn_ended":
			event = agents.Event{Type: agents.Transcript, Role: message.Role, Text: message.Text}
		case "client_tool_call":
			// Tell the agent that client tools are unsupported
			if message.ExpectsResponse {
				s.send(map[string]any{
					"type":         "client_tool_result",
					"tool_call_id": message.ToolCallID,
					"result":       "The bridge does not support client tools",
					"is_error":     true,
				})
			}
			continue
		default:
			// Skip other messages, since fatal errors also close the connection
			continue
		}

		if !s.emit(event) {
			return
		}
	}
}

// emit delivers an event, unless the session is closed.
//
// Parameters:
//   - event: the event to deliver
//
// Returns:
//   - true if the event was delivered, or false if the session is closed
func (s *session) emit(event agents.Event) bool {
	select {
	case s.events <- event:
		return true
	case <-s.closed:
		return false
	}
}

// closeEvent explains why the connection closed. A normal close means the
// agent ended the call, and anything else is a failure.
//
// Parameters:
//   - err: the error from reading the connection
//
// Returns:
//   - SessionEnded for a normal close, or SessionFailed with the error
func closeEvent(err error) agents.Event {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		return agents.Event{Type: agents.SessionEnded}
	}
	return agents.Event{Type: agents.SessionFailed, Err: fmt.Errorf("read from Cartesia: %w", err)}
}

// Close ends the conversation and disconnects from Cartesia.
//
// Returns:
//   - nil, always
func (s *session) Close(context.Context) error {
	s.closeOnce.Do(func() {
		close(s.closed)
		if s.conn == nil {
			return
		}

		// Send a close message, so that Cartesia ends the call
		closing := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		s.conn.WriteControl(websocket.CloseMessage, closing, time.Now().Add(time.Second))
		s.conn.Close()
	})
	return nil
}
