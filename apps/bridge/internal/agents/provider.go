package agents

import "context"

type Provider interface {
	Name() string
	NewSession(ctx context.Context, config SessionConfig) (Session, error)
}

type SessionConfig struct {
	AgentID string
	Audio   AudioFormat
}

type AudioFormat struct {
	Encoding   AudioEncoding
	SampleRate int
	Channels   int
}

type AudioEncoding string

const (
	PCM16LE AudioEncoding = "pcm_s16le"
	MuLaw   AudioEncoding = "mulaw"
)

type Session interface {
	Start(ctx context.Context) error
	SendAudio(ctx context.Context, audio []byte) error
	Events() <-chan Event
	Close(ctx context.Context) error
}

type EventType string

const (
	AudioOutput   EventType = "audio_output"
	ClearAudio    EventType = "clear_audio"
	Transcript    EventType = "transcript"
	SendDTMF      EventType = "send_dtmf"
	EndCall       EventType = "end_call"
	SessionEnded  EventType = "session_ended"
	SessionFailed EventType = "session_failed"
)

type Event struct {
	Type   EventType
	Audio  []byte
	Text   string
	Role   Speaker
	Digits string
	Err    error
}

type Speaker string

const (
	User      Speaker = "user"
	Assistant Speaker = "assistant"
)
