package livekit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"bridge/internal/telephony"

	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/twitchtv/twirp"
)

// Keypad
const keypad = "0123456789*#ABCD"

type transport interface {
	telephony.Audio
	PressKey(code uint32, digit string) error
	WaitReady(ctx context.Context) error
	Done() <-chan struct{}
	Err() error
	Close()
}

type Service struct {
	sip   *lksdk.SIPClient
	rooms *lksdk.RoomServiceClient
	join  func(ctx context.Context, roomName, phoneIdentity string) (transport, error)
}

// New creates a service without contacting the LiveKit server.
func New(url, apiKey, apiSecret string) *Service {
	return &Service{
		sip:   lksdk.NewSIPClient(url, apiKey, apiSecret),
		rooms: lksdk.NewRoomServiceClient(url, apiKey, apiSecret),
		join: func(ctx context.Context, roomName, phoneIdentity string) (transport, error) {
			return joinRoom(ctx, url, apiKey, apiSecret, roomName, phoneIdentity)
		},
	}
}

// DialOutbound calls request.To and returns once someone answers.
//
// The steps are:
//  1. Join the room, so that the agent hears the first word.
//  2. Dial through the trunk and wait for an answer.
//  3. Wait until the phone can hear the agent.
//
// If a step fails, DialOutbound hangs up and leaves the room.
//
// Parameters:
//   - ctx: stops dialing early if it is cancelled or times out
//   - request: the trunk, numbers, and room for the call
//
// Returns:
//   - the answered call
//   - an error if nobody answers or the audio cannot start
func (s *Service) DialOutbound(ctx context.Context, request telephony.OutboundRequest) (telephony.Call, error) {
	info := telephony.Info{
		Direction:           telephony.Outbound,
		From:                request.From,
		To:                  request.To,
		RoomName:            request.RoomName,
		ParticipantIdentity: "phone-" + uuid.NewString(),
	}

	// Join the room, so that the agent hears the first word
	media, err := s.join(ctx, info.RoomName, info.ParticipantIdentity)
	if err != nil {
		return nil, err
	}
	c := &call{transport: media, info: info, rooms: s.rooms}

	// Dial through the trunk and wait for an answer
	participant, err := s.sip.CreateSIPParticipant(ctx, &livekit.CreateSIPParticipantRequest{
		SipTrunkId:          request.TrunkID,
		SipNumber:           request.From,
		SipCallTo:           request.To,
		RoomName:            info.RoomName,
		ParticipantIdentity: info.ParticipantIdentity,
		WaitUntilAnswered:   true,
	})
	if err != nil {
		// Hang up, in case LiveKit placed the call before failing
		return nil, errors.Join(fmt.Errorf("dial: %w", err), c.End(context.WithoutCancel(ctx)))
	}
	c.info.ID = participant.SipCallId

	// Wait until the phone can hear the agent
	if err := media.WaitReady(ctx); err != nil {
		return nil, errors.Join(err, c.End(context.WithoutCancel(ctx)))
	}
	return c, nil
}

// AttachInbound joins the room of a call that LiveKit has already answered.
//
// If the audio cannot start, AttachInbound leaves the caller on the line, so
// that another attempt can answer them.
//
// Parameters:
//   - ctx: stops attaching early if it is cancelled or times out
//   - request: the room and the phone participant that the dispatch rule created
//
// Returns:
//   - the call
//   - an error if the participant is not a phone or the audio cannot start
func (s *Service) AttachInbound(ctx context.Context, request telephony.InboundRequest) (telephony.Call, error) {
	// Find the phone participant, which holds the call's numbers
	participant, err := s.rooms.GetParticipant(ctx, &livekit.RoomParticipantIdentity{
		Room:     request.RoomName,
		Identity: request.ParticipantIdentity,
	})
	if err != nil {
		return nil, fmt.Errorf("get phone participant: %w", err)
	}
	if participant.Kind != livekit.ParticipantInfo_SIP {
		return nil, fmt.Errorf("participant %q is not a phone call", request.ParticipantIdentity)
	}

	// Join the room and wait until the caller can hear the agent
	media, err := s.join(ctx, request.RoomName, request.ParticipantIdentity)
	if err != nil {
		return nil, err
	}
	if err := media.WaitReady(ctx); err != nil {
		media.Close()
		return nil, err
	}

	info := telephony.Info{
		ID:                  participant.Attributes[livekit.AttrSIPCallID],
		Direction:           telephony.Inbound,
		From:                participant.Attributes[livekit.AttrSIPPhoneNumber],
		To:                  participant.Attributes[livekit.AttrSIPTrunkNumber],
		RoomName:            request.RoomName,
		ParticipantIdentity: request.ParticipantIdentity,
	}
	return &call{transport: media, info: info, rooms: s.rooms}, nil
}

type call struct {
	transport
	info  telephony.Info
	rooms *lksdk.RoomServiceClient
}

// Info describes the call.
func (c *call) Info() telephony.Info {
	return c.info
}

// SendDTMF presses keys on the phone keypad, in order. It checks every key
// first, so that a bad sequence sends nothing.
//
// Parameters:
//   - digits: the keys to press, from 0-9, *, #, and A-D
//
// Returns:
//   - nil when every key was pressed
//   - an error if a key is invalid or cannot be sent
func (c *call) SendDTMF(_ context.Context, digits string) error {
	// Check every key first, so that a bad sequence sends nothing
	for _, key := range digits {
		if !strings.ContainsRune(keypad, key) {
			return fmt.Errorf("%q is not a DTMF key", key)
		}
	}

	// Press each key
	for _, key := range digits {
		code := uint32(strings.IndexRune(keypad, key))
		if err := c.PressKey(code, string(key)); err != nil {
			return fmt.Errorf("send DTMF: %w", err)
		}
	}
	return nil
}

// End hangs up by removing the phone from the room, then leaves the room.
//
// It is safe to call more than once, because a phone that already left counts
// as hung up.
//
// Parameters:
//   - ctx: stops the request early if it is cancelled or times out
//
// Returns:
//   - nil when the phone has left the room
//   - an error if LiveKit cannot be reached or rejects the request
func (c *call) End(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Remove the phone, which hangs up the call
	_, err := c.rooms.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{
		Room:     c.info.RoomName,
		Identity: c.info.ParticipantIdentity,
	})
	var twirpErr twirp.Error
	if errors.As(err, &twirpErr) && twirpErr.Code() == twirp.NotFound {
		err = nil
	}

	// Leave the room
	c.Close()

	if err != nil {
		return fmt.Errorf("remove phone participant: %w", err)
	}
	return nil
}
