package inbound

import (
	"net/http"
	"strings"

	"bridge/internal/telephony"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"
)

// Webhook handles LiveKit's webhooks, and calls answer for each phone call
// that the dispatch rule places in a room.
//
// Parameters:
//   - keys: the LiveKit API keys that sign the webhooks
//   - answer: starts answering one call; it must return quickly, because
//     LiveKit waits for the response
//
// Returns:
//   - a handler that rejects webhooks without a valid signature
func Webhook(keys auth.KeyProvider, answer func(telephony.InboundRequest)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that LiveKit signed the event
		event, err := webhook.ReceiveWebhookEvent(r, keys)
		if err != nil {
			http.Error(w, "invalid webhook", http.StatusUnauthorized)
			return
		}

		// Answer a phone that joins one of our rooms
		room := event.GetRoom().GetName()
		participant := event.GetParticipant()
		if event.GetEvent() == webhook.EventParticipantJoined &&
			participant.GetKind() == livekit.ParticipantInfo_SIP &&
			strings.HasPrefix(room, roomPrefix) {
			answer(telephony.InboundRequest{RoomName: room, ParticipantIdentity: participant.GetIdentity()})
		}
	})
}
