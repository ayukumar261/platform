// Package bridge connects inbound and outbound phone calls to agents.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"bridge/internal/agents"
	"bridge/internal/outbound"
	"bridge/internal/relay"
	"bridge/internal/telephony"
)

// Constants
// TODO: Delete when inbound calls choose their own agent
const inboundAgentID = "agent_41pN1ypfNG7t3egkf9rLKq"

type Bridge struct {
	calls    telephony.Service
	agents   agents.Provider
	trunkIDs map[string]string
}

// New prepares the bridge for inbound and outbound calls without starting a call.
//
// Parameters:
//   - calls: attaches incoming calls and dials outgoing calls
//   - provider: creates the agent session for each call
//   - trunkIDs: each carrier's outbound trunk ID, prepared during startup
//
// Returns:
//   - a bridge ready to answer and dial calls
func New(calls telephony.Service, provider agents.Provider, trunkIDs map[string]string) *Bridge {
	return &Bridge{
		calls:    calls,
		agents:   provider,
		trunkIDs: trunkIDs,
	}
}

// Answer connects the inbound agent to a call, and returns when the call ends.
//
// Parameters:
//   - ctx: hangs up early if it is cancelled
//   - request: the room and the phone participant that LiveKit reported
//
// Returns:
//   - nil when the call ends normally
//   - an error if the call cannot be answered or fails
func (b *Bridge) Answer(ctx context.Context, request telephony.InboundRequest) error {
	// Join the call, giving up if it takes too long
	setup, cancel := context.WithTimeout(ctx, 10*time.Second)
	call, err := b.calls.AttachInbound(setup, request)
	cancel()
	if err != nil {
		return fmt.Errorf("attach inbound call: %w", err)
	}

	return b.talk(ctx, call, inboundAgentID)
}

// Dial calls a number through a carrier, connects the requested agent, and
// returns when the call ends.
//
// Parameters:
//   - ctx: hangs up early if it is cancelled
//   - carrier: the carrier to call through, such as "telnyx"
//   - request: the accepted call ID, destination number, and agent
//
// Returns:
//   - nil when the call ends normally
//   - an error if the carrier or provider is unknown, or the call fails
func (b *Bridge) Dial(ctx context.Context, carrier string, request outbound.Request) error {
	trunkID, ok := b.trunkIDs[carrier]
	if !ok {
		return fmt.Errorf("unknown carrier %q", carrier)
	}
	if request.Provider != b.agents.Name() {
		return fmt.Errorf("unknown provider %q", request.Provider)
	}

	// Call the number from a new room, giving up if nobody answers in time
	setup, cancel := context.WithTimeout(ctx, time.Minute)
	call, err := b.calls.DialOutbound(setup, telephony.OutboundRequest{
		TrunkID:  trunkID,
		To:       request.To,
		RoomName: "outbound-" + request.CallID,
	})
	cancel()
	if err != nil {
		return fmt.Errorf("dial outbound call: %w", err)
	}

	return b.talk(ctx, call, request.ProviderAgentID)
}

// talk starts an agent session on a call, and returns when the call ends.
//
// Parameters:
//   - ctx: hangs up early if it is cancelled
//   - call: the answered phone call
//   - agentID: the Cartesia agent that talks on the call
//
// Returns:
//   - nil when the call ends normally
//   - an error if the session cannot start or the call fails
func (b *Bridge) talk(ctx context.Context, call telephony.Call, agentID string) error {
	session, err := b.agents.NewSession(ctx, agents.SessionConfig{AgentID: agentID, Audio: relay.AudioFormat})
	if err != nil {
		return errors.Join(fmt.Errorf("create agent session: %w", err), call.End(ctx))
	}

	return relay.Connect(ctx, call, session, nil)
}
