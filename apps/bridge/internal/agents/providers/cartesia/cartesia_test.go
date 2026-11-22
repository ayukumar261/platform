package cartesia

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"bridge/internal/agents"

	"github.com/gorilla/websocket"
)

// Constants
const testAPIKey = "test-api-key"

// Configuration
var testConfig = agents.SessionConfig{
	AgentID: "agent-test",
	Audio:   agents.AudioFormat{Encoding: agents.PCM16LE, SampleRate: 16000, Channels: 1},
}

// testProvider points a provider at a local server that runs handler.
//
// Parameters:
//   - t: closes the server when the test ends
//   - handler: answers each connection in place of Cartesia
//
// Returns:
//   - a provider that connects to the local server
func testProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	provider := New(testAPIKey)
	provider.endpoint = "ws" + strings.TrimPrefix(server.URL, "http") + "/"
	return provider
}

// fakeCartesia accepts a session like Cartesia does, then runs script.
//
// Parameters:
//   - t: reports connection errors and closes the server when the test ends
//   - script: plays Cartesia's side of the session after session_ready
//
// Returns:
//   - a provider that connects to the fake
func fakeCartesia(t *testing.T, script func(conn *websocket.Conn)) *Provider {
	return testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()

		// Answer session_create with session_ready
		var create map[string]any
		if err := conn.ReadJSON(&create); err != nil {
			t.Error(err)
			return
		}
		if !send(t, conn, map[string]any{"type": "session_ready"}) {
			return
		}

		script(conn)
	})
}

// start creates and starts a session, and closes it when the test ends.
//
// Parameters:
//   - t: fails the test if the session cannot start
//   - provider: creates the session with testConfig
//
// Returns:
//   - a started session
func start(t *testing.T, provider *Provider) agents.Session {
	t.Helper()
	session, err := provider.NewSession(t.Context(), testConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// t.Context() is already cancelled while cleanup runs
		if err := session.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})

	if err := session.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	return session
}

// send writes messages from Cartesia's side, in order.
//
// Parameters:
//   - t: reports the first failed write
//   - conn: the connection to write to
//   - messages: the JSON messages to send
//
// Returns:
//   - true if every message was sent
func send(t *testing.T, conn *websocket.Conn, messages ...map[string]any) bool {
	for _, message := range messages {
		if err := conn.WriteJSON(message); err != nil {
			t.Errorf("send %v: %v", message["type"], err)
			return false
		}
	}
	return true
}

// closeWith closes the connection from Cartesia's side with the given code.
//
// Parameters:
//   - t: reports a failed close
//   - conn: the connection to close
//   - code: the WebSocket close code to send
func closeWith(t *testing.T, conn *websocket.Conn, code int) {
	message := websocket.FormatCloseMessage(code, "")
	if err := conn.WriteControl(websocket.CloseMessage, message, time.Now().Add(time.Second)); err != nil {
		t.Errorf("close with %d: %v", code, err)
	}
}

// collect reads events until the session closes them.
//
// Parameters:
//   - session: the session to read from
//
// Returns:
//   - every event, in the order it arrived
func collect(session agents.Session) []agents.Event {
	var events []agents.Event
	for event := range session.Events() {
		events = append(events, event)
	}
	return events
}

func TestStartSendsCredentialsAndAudioFormat(t *testing.T) {
	provider := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		// Check the agent, the version, and the API key
		if r.URL.Path != "/agent-test" {
			t.Errorf("expected path /agent-test, got %s", r.URL.Path)
		}
		if version := r.URL.Query().Get("cartesia_version"); version != apiVersion {
			t.Errorf("expected version %s, got %q", apiVersion, version)
		}
		if key := r.Header.Get("X-API-Key"); key != testAPIKey {
			t.Errorf("expected the API key, got %q", key)
		}

		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()

		// Check the audio format, then accept the session
		var create struct {
			Type  string `json:"type"`
			Audio struct {
				InputFormat string `json:"input_format"`
			} `json:"audio"`
		}
		if err := conn.ReadJSON(&create); err != nil {
			t.Error(err)
			return
		}
		if create.Type != "session_create" || create.Audio.InputFormat != "pcm_16000" {
			t.Errorf("expected session_create with pcm_16000, got %+v", create)
		}
		send(t, conn, map[string]any{"type": "session_ready"})
	})

	start(t, provider)
}

func TestStartReportsRejectedConnection(t *testing.T) {
	provider := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid API key", http.StatusUnauthorized)
	})
	session, err := provider.NewSession(t.Context(), testConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(t.Context()); err != nil {
			t.Error(err)
		}
	}()

	err = session.Start(t.Context())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected a 401 error, got %v", err)
	}
}

func TestNewSessionRejectsUnsupportedAudio(t *testing.T) {
	config := testConfig
	config.Audio.Channels = 2

	if _, err := New(testAPIKey).NewSession(t.Context(), config); err == nil {
		t.Fatal("expected an error for stereo audio")
	}
}

func TestSendAudioSendsAudioInput(t *testing.T) {
	received := make(chan []byte, 1)
	provider := fakeCartesia(t, func(conn *websocket.Conn) {
		var input struct {
			Type  string `json:"type"`
			Audio []byte `json:"audio"`
		}
		if err := conn.ReadJSON(&input); err != nil {
			t.Error(err)
			return
		}
		if input.Type != "audio_input" {
			t.Errorf("expected audio_input, got %s", input.Type)
		}
		received <- input.Audio
	})
	session := start(t, provider)

	audio := []byte{1, 2, 3, 4}
	if err := session.SendAudio(t.Context(), audio); err != nil {
		t.Fatal(err)
	}
	select {
	case sent := <-received:
		if !bytes.Equal(sent, audio) {
			t.Fatalf("expected %v, got %v", audio, sent)
		}
	case <-time.After(time.Second):
		t.Fatal("audio did not reach Cartesia")
	}
}

func TestEventsTranslateMessages(t *testing.T) {
	provider := fakeCartesia(t, func(conn *websocket.Conn) {
		if !send(t, conn,
			map[string]any{"type": "audio_output", "audio": []byte{1, 2}},
			map[string]any{"type": "audio_output_clear"},
			map[string]any{"type": "dtmf_output", "digit": "#"},
			map[string]any{"type": "turn_ended", "role": "user", "text": "Hello"},
		) {
			return
		}
		closeWith(t, conn, websocket.CloseNormalClosure)
	})
	session := start(t, provider)

	expected := []agents.Event{
		{Type: agents.AudioOutput, Audio: []byte{1, 2}},
		{Type: agents.ClearAudio},
		{Type: agents.SendDTMF, Digits: "#"},
		{Type: agents.Transcript, Role: agents.User, Text: "Hello"},
		{Type: agents.SessionEnded},
	}
	if events := collect(session); !reflect.DeepEqual(events, expected) {
		t.Fatalf("expected %+v, got %+v", expected, events)
	}
}

func TestEventsSkipNonFatalErrors(t *testing.T) {
	provider := fakeCartesia(t, func(conn *websocket.Conn) {
		if !send(t, conn, map[string]any{"type": "error", "code": "invalid_event", "fatal": false}) {
			return
		}
		closeWith(t, conn, websocket.CloseNormalClosure)
	})
	session := start(t, provider)

	expected := []agents.Event{{Type: agents.SessionEnded}}
	if events := collect(session); !reflect.DeepEqual(events, expected) {
		t.Fatalf("expected %+v, got %+v", expected, events)
	}
}

func TestEventsReportAbnormalClose(t *testing.T) {
	provider := fakeCartesia(t, func(conn *websocket.Conn) {
		closeWith(t, conn, websocket.CloseInternalServerErr)
	})
	session := start(t, provider)

	events := collect(session)
	if len(events) != 1 || events[0].Type != agents.SessionFailed || events[0].Err == nil {
		t.Fatalf("expected one SessionFailed with an error, got %+v", events)
	}
}

func TestClientToolCallGetsErrorResult(t *testing.T) {
	result := make(chan map[string]any, 1)
	provider := fakeCartesia(t, func(conn *websocket.Conn) {
		if !send(t, conn, map[string]any{"type": "client_tool_call", "tool_call_id": "tool-1", "expects_response": true}) {
			return
		}
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			t.Error(err)
			return
		}
		result <- message
	})
	start(t, provider)

	select {
	case message := <-result:
		if message["type"] != "client_tool_result" || message["tool_call_id"] != "tool-1" || message["is_error"] != true {
			t.Fatalf("expected an error result for tool-1, got %v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("the tool call did not get a result")
	}
}

func TestCloseStopsEvents(t *testing.T) {
	provider := fakeCartesia(t, func(conn *websocket.Conn) {
		// Wait until the session closes the connection, which ends this read
		var message any
		_ = conn.ReadJSON(&message)
	})
	session := start(t, provider)

	if err := session.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(t.Context()); err != nil {
		t.Fatalf("expected a second Close to succeed, got %v", err)
	}
	for range session.Events() {
	}
}
