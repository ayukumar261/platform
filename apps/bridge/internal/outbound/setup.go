package outbound

import (
	"context"
	"fmt"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

const (
	trunkName = "outbound"
)

// Carriers
var carriers = []string{"telnyx", "twilio"}

// TODO: Remove for PostgreSQL-backed phone numbers
const phoneNumber = "+17627013110"

// Setup makes sure LiveKit has an outbound trunk for each carrier.
//
// It is safe to call on every startup, because it only creates trunks that
// are missing. On a trunk that already exists it only fixes the address. To
// change anything else, delete the trunk so that Setup creates it again.
//
// Parameters:
//   - ctx: stops the requests early if it is cancelled or times out
//   - sipClient: the client used to talk to the LiveKit SIP API
//   - kamailioAddress: where LiveKit SIP sends calls, as "host:port"
//
// Returns:
//   - each carrier's trunk ID, keyed by carrier, such as "telnyx"
//   - an error if LiveKit cannot be reached or rejects a request
func Setup(ctx context.Context, sipClient *lksdk.SIPClient, kamailioAddress string) (map[string]string, error) {
	// Find or create the trunk that sends our calls through each carrier
	trunkIDs := make(map[string]string)
	for _, carrier := range carriers {
		trunkID, err := ensureTrunk(ctx, sipClient, carrier, kamailioAddress)
		if err != nil {
			return nil, fmt.Errorf("ensure outbound trunk for %s: %w", carrier, err)
		}
		trunkIDs[carrier] = trunkID
	}

	return trunkIDs, nil
}

// ensureTrunk finds the carrier's outbound trunk, or creates it if missing.
//
// The trunk is found by its name, such as "outbound-telnyx". Its calls go to
// Kamailio with the carrier's name in the X-Carrier header, which tells
// Kamailio where to send them.
//
// Parameters:
//   - ctx: stops the requests early if it is cancelled or times out
//   - sipClient: the client used to talk to the LiveKit SIP API
//   - carrier: the carrier that the trunk places calls through
//   - kamailioAddress: where LiveKit SIP sends calls, as "host:port"
//
// Returns:
//   - the trunk ID, whether the trunk was found or created
//   - an error if LiveKit cannot be reached or rejects a request
func ensureTrunk(ctx context.Context, sipClient *lksdk.SIPClient, carrier, kamailioAddress string) (string, error) {
	name := trunkName + "-" + carrier

	// Ask LiveKit for every outbound trunk
	existing, err := sipClient.ListSIPOutboundTrunk(ctx, &livekit.ListSIPOutboundTrunkRequest{})
	if err != nil {
		return "", err
	}

	// Reuse the trunk if it already exists
	for _, trunk := range existing.Items {
		if trunk.Name != name {
			continue
		}

		// Point the trunk at Kamailio if it points somewhere else
		if trunk.Address != kamailioAddress {
			_, err := sipClient.UpdateSIPOutboundTrunk(ctx, &livekit.UpdateSIPOutboundTrunkRequest{
				SipTrunkId: trunk.SipTrunkId,
				Action: &livekit.UpdateSIPOutboundTrunkRequest_Update{
					Update: &livekit.SIPOutboundTrunkUpdate{Address: &kamailioAddress},
				},
			})
			if err != nil {
				return "", err
			}
		}
		return trunk.SipTrunkId, nil
	}

	// Create a trunk that sends calls to Kamailio
	created, err := sipClient.CreateSIPOutboundTrunk(ctx, &livekit.CreateSIPOutboundTrunkRequest{
		Trunk: &livekit.SIPOutboundTrunkInfo{
			Name:    name,
			Address: kamailioAddress,
			Numbers: []string{phoneNumber},
			Headers: map[string]string{"X-Carrier": carrier},
		},
	})
	if err != nil {
		return "", err
	}
	return created.SipTrunkId, nil
}
