package outbound

import (
	"context"
	"fmt"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

const trunkName = "outbound"

// TODO: Remove for PostgreSQL-backed phone numbers
const phoneNumber = "+17627013110"

// Setup makes sure LiveKit can place calls from phoneNumber through Kamailio.
//
// There is only one step: find the outbound trunk, or create it if it is
// missing. Outbound calls need no dispatch rule, because the room is chosen
// when the call is placed.
//
// Setup only creates what is missing, so it is safe to call on every startup.
// It does not update a trunk that already exists: to change it, delete it so
// that Setup can create it again.
//
// Parameters:
//   - ctx: stops the requests early if it is cancelled or times out
//   - sipClient: the client used to talk to the LiveKit SIP API
//   - kamailioAddress: where LiveKit SIP sends calls, as "host:port"
//
// Returns:
//   - the trunk ID, which is needed later to place a call
//   - an error if LiveKit cannot be reached or rejects a request
func Setup(ctx context.Context, sipClient *lksdk.SIPClient, kamailioAddress string) (string, error) {
	// Find or create the trunk that sends our calls
	trunkID, err := ensureTrunk(ctx, sipClient, kamailioAddress)
	if err != nil {
		return "", fmt.Errorf("ensure outbound trunk: %w", err)
	}

	return trunkID, nil
}

// ensureTrunk returns the ID of the outbound trunk, creating the trunk first
// if LiveKit does not have one yet.
//
// The trunk is recognised by its name, "outbound". Calls placed through it
// are sent to Kamailio and show phoneNumber as the caller.
//
// Parameters:
//   - ctx: stops the requests early if it is cancelled or times out
//   - sipClient: the client used to talk to the LiveKit SIP API
//   - kamailioAddress: where LiveKit SIP sends calls, as "host:port"
//
// Returns:
//   - the trunk ID, whether the trunk was found or created
//   - an error if LiveKit cannot be reached or rejects a request
func ensureTrunk(ctx context.Context, sipClient *lksdk.SIPClient, kamailioAddress string) (string, error) {
	// Ask LiveKit for every outbound trunk
	existing, err := sipClient.ListSIPOutboundTrunk(ctx, &livekit.ListSIPOutboundTrunkRequest{})
	if err != nil {
		return "", err
	}

	// Reuse the trunk if it already exists
	for _, trunk := range existing.Items {
		if trunk.Name == trunkName {
			return trunk.SipTrunkId, nil
		}
	}

	// Create a trunk that sends calls to Kamailio
	created, err := sipClient.CreateSIPOutboundTrunk(ctx, &livekit.CreateSIPOutboundTrunkRequest{
		Trunk: &livekit.SIPOutboundTrunkInfo{
			Name:    trunkName,
			Address: kamailioAddress,
			Numbers: []string{phoneNumber},
		},
	})
	if err != nil {
		return "", err
	}
	return created.SipTrunkId, nil
}
