package clients

import lksdk "github.com/livekit/server-sdk-go/v2"

// NewLiveKitSIPClient creates a client for the LiveKit SIP API without contacting LiveKit.
//
// Parameters:
//   - url: the LiveKit server URL
//   - apiKey: identifies the bridge to LiveKit
//   - apiSecret: signs the access token sent with each request
//
// Returns:
//   - a client that manages the SIP trunks and dispatch rules
func NewLiveKitSIPClient(url, apiKey, apiSecret string) *lksdk.SIPClient {
	return lksdk.NewSIPClient(url, apiKey, apiSecret)
}
