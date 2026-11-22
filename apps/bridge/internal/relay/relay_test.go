package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"bridge/internal/agents"
	"bridge/internal/telephony"
)

type fakeCall struct {
	audio   chan []byte
	done    chan struct{}
	endErr  error
	actions []string
}

// ReadAudio returns queued caller audio, or io.EOF once the call ends.
//
// Parameters:
//   - ctx: stops waiting if it ends first
//
// Returns:
//   - the next queued audio
//   - io.EOF once the call ends, or ctx's error if it ends first
func (c *fakeCall) ReadAudio(ctx context.Context) ([]byte, error) {
	select {
	case audio := <-c.audio:
		return audio, nil
	case <-c.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// WriteAudio records a write.
//
// Returns:
//   - nil, always
func (c *fakeCall) WriteAudio(context.Context, []byte) error {
	c.actions = append(c.actions, "write")
	return nil
}

// ClearAudio records a clear.
//
// Returns:
//   - nil, always
func (c *fakeCall) ClearAudio(context.Context) error {
	c.actions = append(c.actions, "clear")
	return nil
}

// DrainAudio records a drain.
//
// Returns:
//   - nil, always
func (c *fakeCall) DrainAudio(context.Context) error {
	c.actions = append(c.actions, "drain")
	return nil
}

// SendDTMF records the digits.
//
// Parameters:
//   - digits: the keys to record
//
// Returns:
//   - nil, always
func (c *fakeCall) SendDTMF(_ context.Context, digits string) error {
	c.actions = append(c.actions, "dtmf "+digits)
	return nil
}

// End records the hangup and ends the call.
//
// Returns:
//   - endErr, which is nil unless the test sets it
func (c *fakeCall) End(context.Context) error {
	c.actions = append(c.actions, "end")
	c.hangUp()
	return c.endErr
}

// Info returns empty call details.
//
// Returns:
//   - an empty Info
func (c *fakeCall) Info() telephony.Info { return telephony.Info{} }

// Done closes once the call ends.
//
// Returns:
//   - a channel that closes once the call ends
func (c *fakeCall) Done() <-chan struct{} { return c.done }

// Err returns nil, because the fake call never fails.
//
// Returns:
//   - nil, always
func (c *fakeCall) Err() error { return nil }

// hangUp ends the call, as the caller hanging up would.
func (c *fakeCall) hangUp() {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
}

type fakeSession struct {
	startErr error
	events   chan agents.Event
	sent     chan []byte
	closed   bool
}

// Start pretends to connect to the agent.
//
// Returns:
//   - startErr, which is nil unless the test sets it
func (s *fakeSession) Start(context.Context) error { return s.startErr }

// Events delivers the queued agent events.
//
// Returns:
//   - the channel of queued events
func (s *fakeSession) Events() <-chan agents.Event { return s.events }

// SendAudio passes the audio to sent, or stops when ctx ends.
//
// Parameters:
//   - ctx: stops waiting if it ends first
//   - audio: the audio to pass to sent
//
// Returns:
//   - nil once sent receives the audio
//   - ctx's error if it ends first
func (s *fakeSession) SendAudio(ctx context.Context, audio []byte) error {
	select {
	case s.sent <- audio:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close records that the session closed.
//
// Returns:
//   - nil, always
func (s *fakeSession) Close(context.Context) error {
	s.closed = true
	return nil
}

// fakes returns a call and a session with the given agent events queued.
//
// Parameters:
//   - events: the events the session delivers, in order
//
// Returns:
//   - a call that is still connected
//   - a session with the events queued
func fakes(events ...agents.Event) (*fakeCall, *fakeSession) {
	call := &fakeCall{audio: make(chan []byte, 1), done: make(chan struct{})}
	session := &fakeSession{events: make(chan agents.Event, len(events)), sent: make(chan []byte, 1)}
	for _, event := range events {
		session.events <- event
	}
	return call, session
}

func TestConnectCarriesOutAgentEvents(t *testing.T) {
	call, session := fakes(
		agents.Event{Type: agents.AudioOutput, Audio: []byte{1, 2}},
		agents.Event{Type: agents.ClearAudio},
		agents.Event{Type: agents.SendDTMF, Digits: "1#"},
		agents.Event{Type: agents.SessionEnded},
	)

	if err := Connect(t.Context(), call, session, nil); err != nil {
		t.Fatal(err)
	}

	expected := []string{"write", "clear", "dtmf 1#", "drain", "end"}
	if !slices.Equal(call.actions, expected) {
		t.Errorf("expected %v, got %v", expected, call.actions)
	}
	if !session.closed {
		t.Error("expected the session to be closed")
	}
}

func TestConnectPassesTranscripts(t *testing.T) {
	call, session := fakes(
		agents.Event{Type: agents.Transcript, Role: agents.User, Text: "Hello"},
		agents.Event{Type: agents.SessionEnded},
	)

	var transcripts []string
	onTranscript := func(event agents.Event) {
		transcripts = append(transcripts, string(event.Role)+": "+event.Text)
	}
	if err := Connect(t.Context(), call, session, onTranscript); err != nil {
		t.Fatal(err)
	}

	if expected := []string{"user: Hello"}; !slices.Equal(transcripts, expected) {
		t.Errorf("expected %v, got %v", expected, transcripts)
	}
}

func TestConnectForwardsCallerAudio(t *testing.T) {
	call, session := fakes()
	result := make(chan error, 1)
	go func() { result <- Connect(t.Context(), call, session, nil) }()

	call.audio <- []byte{1, 2}
	select {
	case audio := <-session.sent:
		if !bytes.Equal(audio, []byte{1, 2}) {
			t.Errorf("expected [1 2], got %v", audio)
		}
	case <-time.After(time.Second):
		t.Fatal("caller audio did not reach the agent")
	}

	call.hangUp()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestConnectClosesSessionWhenCallerHangsUp(t *testing.T) {
	call, session := fakes()
	call.hangUp()

	if err := Connect(t.Context(), call, session, nil); err != nil {
		t.Fatal(err)
	}
	if !session.closed {
		t.Error("expected the session to be closed")
	}
}

func TestConnectHangsUpWhenStartFails(t *testing.T) {
	call, session := fakes()
	session.startErr = errors.New("agent unavailable")

	if err := Connect(t.Context(), call, session, nil); !errors.Is(err, session.startErr) {
		t.Fatalf("expected %v, got %v", session.startErr, err)
	}
	if !slices.Equal(call.actions, []string{"end"}) || !session.closed {
		t.Errorf("expected the call to end and the session to close, got %v", call.actions)
	}
}

func TestConnectHangsUpWhenAgentFails(t *testing.T) {
	failure := errors.New("agent failed")
	call, session := fakes(agents.Event{Type: agents.SessionFailed, Err: failure})

	if err := Connect(t.Context(), call, session, nil); !errors.Is(err, failure) {
		t.Fatalf("expected %v, got %v", failure, err)
	}
	if !slices.Equal(call.actions, []string{"end"}) || !session.closed {
		t.Errorf("expected the call to end and the session to close, got %v", call.actions)
	}
}

func TestConnectHangsUpWhenCancelled(t *testing.T) {
	call, session := fakes()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := Connect(ctx, call, session, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if !slices.Equal(call.actions, []string{"end"}) || !session.closed {
		t.Errorf("expected the call to end and the session to close, got %v", call.actions)
	}
}

func TestConnectReportsFailedHangUp(t *testing.T) {
	call, session := fakes(agents.Event{Type: agents.SessionEnded})
	call.endErr = errors.New("hang up failed")

	if err := Connect(t.Context(), call, session, nil); !errors.Is(err, call.endErr) {
		t.Fatalf("expected %v, got %v", call.endErr, err)
	}
}
