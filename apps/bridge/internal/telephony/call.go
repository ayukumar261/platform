package telephony

import "context"

type Service interface {
	DialOutbound(ctx context.Context, request OutboundRequest) (Call, error)
	AttachInbound(ctx context.Context, request InboundRequest) (Call, error)
}

type OutboundRequest struct {
	TrunkID  string
	From     string
	To       string
	RoomName string
}

type InboundRequest struct {
	RoomName            string
	ParticipantIdentity string
}

type Call interface {
	Control
	Audio
	Info() Info
	Done() <-chan struct{}
	Err() error
}

type Control interface {
	SendDTMF(ctx context.Context, digits string) error
	End(ctx context.Context) error
}

type Audio interface {
	ReadAudio(ctx context.Context) ([]byte, error)
	WriteAudio(ctx context.Context, audio []byte) error
	ClearAudio(ctx context.Context) error
	DrainAudio(ctx context.Context) error
}

type Info struct {
	ID                  string
	Direction           Direction
	From                string
	To                  string
	RoomName            string
	ParticipantIdentity string
}

type Direction string

const (
	Inbound  Direction = "inbound"
	Outbound Direction = "outbound"
)
