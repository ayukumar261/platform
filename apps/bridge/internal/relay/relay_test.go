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

func (c *fakeCall) WriteAudio(context.Context, []byte) error {
	c.actions = append(c.actions, "write")
	return nil
}

func (c *fakeCall) ClearAudio(context.Context) error {
	c.actions = append(c.actions, "clear")
	return nil
}

func (c *fakeCall) DrainAudio(context.Context) error {
	c.actions = append(c.actions, "drain")
	return nil
}

func (c *fakeCall) SendDTMF(_ context.Context, digits string) error {
	c.actions = append(c.actions, "dtmf "+digits)
	return nil
}

func (c *fakeCall) End(context.Context) error {
	c.actions = append(c.actions, "end")
	c.hangUp()
	return c.endErr
}

func (c *fakeCall) Info() telephony.Info  { return telephony.Info{} }
func (c *fakeCall) Done() <-chan struct{} { return c.done }
func (c *fakeCall) Err() error            { return nil }

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

func (s *fakeSession) Start(context.Context) error { return s.startErr }
func (s *fakeSession) Events() <-chan agents.Event { return s.events }

func (s *fakeSession) SendAudio(ctx context.Context, audio []byte) error {
	select {
	case s.sent <- audio:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *fakeSession) Close(context.Context) error {
	s.closed = true
	return nil
}

// fakes returns a call and a session with the given agent events queued.
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
