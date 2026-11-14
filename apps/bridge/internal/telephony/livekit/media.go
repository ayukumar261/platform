package livekit

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"
	"github.com/livekit/media-sdk"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"
	"github.com/pion/webrtc/v4"
)

// Audio Format
const (
	sampleRate = 16000
	channels   = 1
)

type connection struct {
	room   *lksdk.Room
	phone  string
	output *lkmedia.PCMLocalTrack
	input  audioInput

	ready     chan struct{}
	readyOnce sync.Once
	done      chan struct{}
	doneOnce  sync.Once
	err       error
}

// joinRoom connects the agent to a call's room and publishes its audio.
//
// Audio only flows between the agent and the phone participant, so the room
// should hold just this one call.
//
// Parameters:
//   - ctx: stops joining early if it is cancelled or times out
//   - url, apiKey, apiSecret: the LiveKit server and its credentials
//   - roomName: the room that carries the call
//   - phoneIdentity: the phone participant, who may join after the agent
//
// Returns:
//   - the connection; WaitReady reports when the phone can hear the agent
//   - an error if the agent cannot join the room or publish its audio
func joinRoom(ctx context.Context, url, apiKey, apiSecret, roomName, phoneIdentity string) (transport, error) {
	c := &connection{
		phone: phoneIdentity,
		input: make(audioInput, 10),
		ready: make(chan struct{}),
		done:  make(chan struct{}),
	}

	// Create the agent's audio track, which plays silence while nothing is queued
	output, err := lkmedia.NewPCMLocalTrack(sampleRate, channels, logger.GetLogger())
	if err != nil {
		return nil, fmt.Errorf("create agent track: %w", err)
	}
	c.output = output

	// Follow the phone's audio and the call's end
	callback := lksdk.NewRoomCallback()
	callback.OnTrackSubscribed = c.listen
	callback.OnLocalTrackSubscribed = func(*lksdk.LocalTrackPublication, *lksdk.LocalParticipant) {
		c.readyOnce.Do(func() { close(c.ready) })
	}
	callback.OnParticipantDisconnected = func(participant *lksdk.RemoteParticipant) {
		if participant.Identity() == c.phone {
			c.finish(nil)
		}
	}
	callback.OnDisconnected = func() {
		c.finish(errors.New("disconnected from LiveKit"))
	}

	// Join the room
	c.room = lksdk.NewRoom(callback)
	err = c.room.JoinWithContext(ctx, url, lksdk.ConnectInfo{
		APIKey:              apiKey,
		APISecret:           apiSecret,
		RoomName:            roomName,
		ParticipantIdentity: "agent-" + uuid.NewString(),
	})
	if err != nil {
		output.Close()
		return nil, fmt.Errorf("join room: %w", err)
	}

	// Publish the agent's audio
	_, err = c.room.LocalParticipant.PublishTrack(output, &lksdk.TrackPublicationOptions{Name: "agent"})
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("publish agent track: %w", err)
	}

	return c, nil
}

// listen decodes the phone's audio into c.input once LiveKit subscribes to it.
//
// Parameters:
//   - track: the subscribed track, which is skipped unless it is the phone's audio
//   - participant: the participant who published the track
func (c *connection) listen(track *webrtc.TrackRemote, _ *lksdk.RemoteTrackPublication, participant *lksdk.RemoteParticipant) {
	if participant.Identity() != c.phone || track.Kind() != webrtc.RTPCodecTypeAudio {
		return
	}

	// Decode the phone's audio until the call ends
	_, err := lkmedia.NewPCMRemoteTrack(track, c.input, lkmedia.WithTargetSampleRate(sampleRate))
	if err != nil {
		c.finish(fmt.Errorf("decode phone audio: %w", err))
	}
}

// WaitReady waits until the phone can hear the agent, so that the agent's
// first words are not lost.
//
// Parameters:
//   - ctx: stops waiting early if it is cancelled or times out
//
// Returns:
//   - nil once the phone can hear the agent
//   - an error if the phone leaves first or ctx ends
func (c *connection) WaitReady(ctx context.Context) error {
	select {
	case <-c.ready:
		return nil
	case <-c.done:
		return errors.New("phone left before the audio started")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReadAudio returns the phone's next chunk of audio.
//
// Parameters:
//   - ctx: stops waiting early if it is cancelled
//
// Returns:
//   - the next chunk of audio
//   - io.EOF once the call ends, or an error if ctx ends
func (c *connection) ReadAudio(ctx context.Context) ([]byte, error) {
	select {
	case audio := <-c.input:
		return audio, nil
	case <-c.done:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// WriteAudio queues the agent's audio behind any audio still playing.
//
// Parameters:
//   - audio: the agent's audio, in the format of telephony.Audio
//
// Returns:
//   - nil once the audio is queued
//   - an error if the call has ended
func (c *connection) WriteAudio(_ context.Context, audio []byte) error {
	sample := make(media.PCM16Sample, len(audio)/2)
	for i := range sample {
		sample[i] = int16(binary.LittleEndian.Uint16(audio[2*i:]))
	}
	return c.output.WriteSample(sample)
}

// ClearAudio drops the agent's queued audio.
func (c *connection) ClearAudio(context.Context) error {
	c.output.ClearQueue()
	return nil
}

// DrainAudio waits until the agent's queued audio has played.
//
// Parameters:
//   - ctx: drops the rest of the audio if it ends first
//
// Returns:
//   - nil once the audio has played
//   - an error if ctx ends first
func (c *connection) DrainAudio(ctx context.Context) error {
	played := make(chan struct{})
	go func() {
		c.output.WaitForPlayout()
		close(played)
	}()

	select {
	case <-played:
		return nil
	case <-ctx.Done():
		// Drop the rest, which also stops WaitForPlayout
		c.output.ClearQueue()
		return ctx.Err()
	}
}

// PressKey presses one key on the phone keypad.
func (c *connection) PressKey(code uint32, digit string) error {
	return c.room.LocalParticipant.PublishDTMF(code, digit)
}

// Done closes once the call ends.
func (c *connection) Done() <-chan struct{} {
	return c.done
}

// Err returns why the call failed, or nil if it has not failed.
func (c *connection) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Close leaves the room.
func (c *connection) Close() {
	c.finish(nil)
}

// finish ends the call once, and remembers why.
//
// Parameters:
//   - err: why the call failed, or nil for a normal hangup
func (c *connection) finish(err error) {
	c.doneOnce.Do(func() {
		c.err = err
		close(c.done)
		c.output.Close()

		// Disconnect in the background, because callbacks must not block
		go c.room.Disconnect()
	})
}

type audioInput chan []byte

// WriteSample queues one chunk of the phone's audio for ReadAudio. If
// ReadAudio has fallen behind, as it does before the conversation starts, the
// chunk is dropped.
//
// Parameters:
//   - sample: the phone's decoded audio
//
// Returns:
//   - nil, even when the chunk is dropped
func (input audioInput) WriteSample(sample media.PCM16Sample) error {
	audio := make([]byte, 2*len(sample))
	for i, value := range sample {
		binary.LittleEndian.PutUint16(audio[2*i:], uint16(value))
	}

	select {
	case input <- audio:
	default:
	}
	return nil
}

// Close does nothing, because ReadAudio stops when the call ends.
func (audioInput) Close() error {
	return nil
}
