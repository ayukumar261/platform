package inbound

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"bridge/internal/telephony"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"
	"google.golang.org/protobuf/encoding/protojson"
)

// Constants
const (
	testAPIKey    = "test-api-key"
	testAPISecret = "test-api-secret"
)

// sendWebhook posts an event signed with secret, and returns the status and answers.
func sendWebhook(t *testing.T, event *livekit.WebhookEvent, secret string) (int, []telephony.InboundRequest) {
	t.Helper()
	body, err := protojson.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}

	// Sign a checksum of the body with the API secret
	checksum := sha256.Sum256(body)
	token, err := auth.NewAccessToken(testAPIKey, secret).
		SetSha256(base64.StdEncoding.EncodeToString(checksum[:])).
		ToJWT()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/livekit/webhook", bytes.NewReader(body))
	request.Header.Set("Authorization", token)

	var answered []telephony.InboundRequest
	handler := Webhook(auth.NewSimpleKeyProvider(testAPIKey, testAPISecret), func(request telephony.InboundRequest) {
		answered = append(answered, request)
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code, answered
}

// phoneJoined returns the event LiveKit sends when a phone joins a room.
func phoneJoined(roomName string) *livekit.WebhookEvent {
	return &livekit.WebhookEvent{
		Event:       webhook.EventParticipantJoined,
		Room:        &livekit.Room{Name: roomName},
		Participant: &livekit.ParticipantInfo{Identity: "sip_caller", Kind: livekit.ParticipantInfo_SIP},
	}
}

func TestWebhookAnswersPhoneInCallRoom(t *testing.T) {
	status, answered := sendWebhook(t, phoneJoined("call-test"), testAPISecret)

	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}
	expected := telephony.InboundRequest{RoomName: "call-test", ParticipantIdentity: "sip_caller"}
	if len(answered) != 1 || answered[0] != expected {
		t.Fatalf("expected to answer %+v, got %+v", expected, answered)
	}
}

func TestWebhookIgnoresAgentJoining(t *testing.T) {
	event := phoneJoined("call-test")
	event.Participant.Kind = livekit.ParticipantInfo_STANDARD

	if _, answered := sendWebhook(t, event, testAPISecret); len(answered) != 0 {
		t.Fatalf("expected no answer, got %+v", answered)
	}
}

func TestWebhookIgnoresPhoneInOtherRoom(t *testing.T) {
	if _, answered := sendWebhook(t, phoneJoined("outbound-test"), testAPISecret); len(answered) != 0 {
		t.Fatalf("expected no answer, got %+v", answered)
	}
}

func TestWebhookIgnoresOtherEvents(t *testing.T) {
	event := phoneJoined("call-test")
	event.Event = webhook.EventParticipantLeft

	if _, answered := sendWebhook(t, event, testAPISecret); len(answered) != 0 {
		t.Fatalf("expected no answer, got %+v", answered)
	}
}

func TestWebhookRejectsWrongSignature(t *testing.T) {
	status, answered := sendWebhook(t, phoneJoined("call-test"), "wrong-secret")

	if status != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", status)
	}
	if len(answered) != 0 {
		t.Fatalf("expected no answer, got %+v", answered)
	}
}
