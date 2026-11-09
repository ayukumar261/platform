package clients

import lksdk "github.com/livekit/server-sdk-go/v2"

// NewLiveKitSIPClient creates a client without contacting the LiveKit server.
func NewLiveKitSIPClient(url, apiKey, apiSecret string) *lksdk.SIPClient {
	return lksdk.NewSIPClient(url, apiKey, apiSecret)
}
