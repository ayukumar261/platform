package inbound

import (
	"context"
	"fmt"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

const (
	trunkName        = "inbound"
	dispatchRuleName = "inbound"
	roomPrefix       = "call-"
)

// TODO: Remove for PostgreSQL-backed phone numbers
const phoneNumber = "+17627013110"

// Setup makes sure LiveKit accepts calls to phoneNumber and places each one
// in its own room.
//
// The steps are:
//  1. Find the inbound trunk, or create it if it is missing.
//  2. Find the dispatch rule, or create it if it is missing.
//
// The order matters because the dispatch rule refers to the trunk by its ID.
//
// Setup only creates what is missing, so it is safe to call on every startup.
// It does not update records that already exist: to change one, delete it so
// that Setup can create it again.
//
// Parameters:
//   - ctx: stops the requests early if it is cancelled or times out
//   - sipClient: the client used to talk to the LiveKit SIP API
//
// Returns:
//   - nil when both records exist, whether they were found or created
//   - an error if LiveKit cannot be reached or rejects a request
func Setup(ctx context.Context, sipClient *lksdk.SIPClient) error {
	// Find or create the trunk that accepts our calls
	trunkID, err := ensureTrunk(ctx, sipClient)
	if err != nil {
		return fmt.Errorf("ensure inbound trunk: %w", err)
	}

	// Find or create the rule that gives each call a room
	if err := ensureDispatchRule(ctx, sipClient, trunkID); err != nil {
		return fmt.Errorf("ensure dispatch rule: %w", err)
	}

	return nil
}

// ensureTrunk returns the ID of the inbound trunk, creating the trunk first if
// LiveKit does not have one yet.
//
// The trunk is recognised by its name, "inbound". LiveKit refuses to create a
// trunk that would accept calls from anyone to any number, so ours is limited
// to calls dialled to phoneNumber.
//
// Parameters:
//   - ctx: stops the requests early if it is cancelled or times out
//   - sipClient: the client used to talk to the LiveKit SIP API
//
// Returns:
//   - the trunk ID, whether the trunk was found or created
//   - an error if LiveKit cannot be reached or rejects a request
func ensureTrunk(ctx context.Context, sipClient *lksdk.SIPClient) (string, error) {
	// Ask LiveKit for every inbound trunk
	existing, err := sipClient.ListSIPInboundTrunk(ctx, &livekit.ListSIPInboundTrunkRequest{})
	if err != nil {
		return "", err
	}

	// Reuse the trunk if it already exists
	for _, trunk := range existing.Items {
		if trunk.Name == trunkName {
			return trunk.SipTrunkId, nil
		}
	}

	// Create a trunk that accepts calls to our number
	created, err := sipClient.CreateSIPInboundTrunk(ctx, &livekit.CreateSIPInboundTrunkRequest{
		Trunk: &livekit.SIPInboundTrunkInfo{
			Name:    trunkName,
			Numbers: []string{phoneNumber},
		},
	})
	if err != nil {
		return "", err
	}
	return created.SipTrunkId, nil
}

// ensureDispatchRule creates the dispatch rule for the trunk with the given
// ID, unless LiveKit already has one.
//
// The rule is recognised by its name, "inbound". It puts each call in a new
// room whose name starts with "call-", so two participants never end up in
// the same room.
//
// Parameters:
//   - ctx: stops the requests early if it is cancelled or times out
//   - sipClient: the client used to talk to the LiveKit SIP API
//   - trunkID: the ID of the trunk whose calls the rule applies to
//
// Returns:
//   - nil when the rule exists, whether it was found or created
//   - an error if LiveKit cannot be reached or rejects a request
func ensureDispatchRule(ctx context.Context, sipClient *lksdk.SIPClient, trunkID string) error {
	// Ask LiveKit for every dispatch rule
	existing, err := sipClient.ListSIPDispatchRule(ctx, &livekit.ListSIPDispatchRuleRequest{})
	if err != nil {
		return err
	}

	// Stop if the rule already exists
	for _, rule := range existing.Items {
		if rule.Name == dispatchRuleName {
			return nil
		}
	}

	// Create a rule that puts each call in its own room
	_, err = sipClient.CreateSIPDispatchRule(ctx, &livekit.CreateSIPDispatchRuleRequest{
		DispatchRule: &livekit.SIPDispatchRuleInfo{
			Name:     dispatchRuleName,
			TrunkIds: []string{trunkID},
			Rule: &livekit.SIPDispatchRule{
				Rule: &livekit.SIPDispatchRule_DispatchRuleIndividual{
					DispatchRuleIndividual: &livekit.SIPDispatchRuleIndividual{
						RoomPrefix: roomPrefix,
					},
				},
			},
		},
	})
	return err
}
