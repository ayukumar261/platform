package relay

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"bridge/internal/agents"
	"bridge/internal/telephony"
)

// Audio Format
var AudioFormat = agents.AudioFormat{Encoding: agents.PCM16LE, SampleRate: 16000, Channels: 1}

// Connect lets an agent talk on a call until one of them hangs up, something
// fails, or ctx is cancelled. Either way, it closes the session and hangs up.
//
// The steps are:
//  1. Start the session, so that the agent is ready before the caller is heard.
//  2. Send the caller's audio to the agent in the background.
//  3. Act on the agent's events until the call or the session ends.
//
// When the agent ends the conversation, Connect lets its last words play before
// hanging up.
//
// Parameters:
//   - ctx: ends the conversation early if it is cancelled
//   - call: the answered phone call
//   - session: an unstarted session configured with AudioFormat
//   - onTranscript: receives each finished turn, or nil to ignore them. It
//     runs on the conversation's goroutine, so it must return quickly.
//
// Returns:
//   - nil when the call or the agent ends normally
//   - an error if either side fails or ctx is cancelled
func Connect(ctx context.Context, call telephony.Call, session agents.Session, onTranscript func(agents.Event)) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	var forwarding sync.WaitGroup

	// Close the session and hang up, however the conversation ends
	defer func() {
		cancel()
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		err = errors.Join(err, session.Close(cleanup), call.End(cleanup))
		forwarding.Wait()
	}()

	// Start the agent before listening to the caller
	if err := session.Start(ctx); err != nil {
		return fmt.Errorf("start agent session: %w", err)
	}

	// Send the caller's audio to the agent in the background
	forwarding.Go(func() { forwardAudio(ctx, call, session) })

	// Act on the agent's events until the call or the session ends
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-call.Done():
			return call.Err()
		case event, ok := <-session.Events():
			if !ok {
				return errors.New("agent session closed without saying why")
			}
			if finished, err := handle(ctx, call, event, onTranscript); finished || err != nil {
				return err
			}
		}
	}
}

// handle carries out one agent event on the call.
//
// Parameters:
//   - ctx: stops the action early if it is cancelled
//   - call: the call to act on
//   - event: the agent's event
//   - onTranscript: receives Transcript events, or nil to ignore them
//
// Returns:
//   - whether the conversation is finished
//   - an error if the call failed to carry out the event, or the agent failed
func handle(ctx context.Context, call telephony.Call, event agents.Event, onTranscript func(agents.Event)) (bool, error) {
	switch event.Type {
	case agents.AudioOutput:
		return false, call.WriteAudio(ctx, event.Audio)
	case agents.ClearAudio:
		return false, call.ClearAudio(ctx)
	case agents.SendDTMF:
		return false, call.SendDTMF(ctx, event.Digits)
	case agents.Transcript:
		if onTranscript != nil {
			onTranscript(event)
		}
		return false, nil
	case agents.EndCall, agents.SessionEnded:
		// Let the agent's last words play, but not forever
		drain, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		return true, call.DrainAudio(drain)
	case agents.SessionFailed:
		return true, fmt.Errorf("agent session failed: %w", event.Err)
	default:
		return false, nil
	}
}

// forwardAudio sends the caller's audio to the agent until either side stops.
// It does not report errors, because Connect learns why from the call, the
// session's events, or ctx.
//
// Parameters:
//   - ctx: stops forwarding when it is cancelled
//   - call: the call to read the caller's audio from
//   - session: the session to send the audio to
func forwardAudio(ctx context.Context, call telephony.Audio, session agents.Session) {
	for {
		audio, err := call.ReadAudio(ctx)
		if err != nil {
			return
		}
		if err := session.SendAudio(ctx, audio); err != nil {
			return
		}
	}
}
