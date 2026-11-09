package clients_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bridge/internal/clients"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

func TestNewLiveKitSIPClient(t *testing.T) {
	const apiKey = "test-api-key"
	const apiSecret = "test-api-secret"

	for _, scheme := range []string{"http", "ws"} {
		t.Run(scheme, func(t *testing.T) {
			want := &livekit.ListSIPInboundTrunkResponse{
				Items: []*livekit.SIPInboundTrunkInfo{{SipTrunkId: "test-trunk"}},
			}
			body, err := proto.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}

			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/twirp/livekit.SIP/ListSIPInboundTrunk" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}

				token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
				if !ok {
					t.Error("expected bearer authentication")
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				verifier, err := auth.ParseAPIToken(token)
				if err != nil {
					t.Errorf("parse authentication token: %v", err)
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				if verifier.APIKey() != apiKey {
					t.Error("authentication token does not use the supplied API key")
				}
				_, grants, err := verifier.Verify(apiSecret)
				if err != nil {
					t.Errorf("verify authentication token with the supplied secret: %v", err)
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				if grants.SIP == nil || !grants.SIP.Admin {
					t.Error("expected SIP admin permission for listing trunks")
				}

				w.Header().Set("Content-Type", "application/protobuf")
				if _, err := w.Write(body); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			t.Cleanup(server.Close)

			url := strings.Replace(server.URL, "http://", scheme+"://", 1)
			client := clients.NewLiveKitSIPClient(url, apiKey, apiSecret)
			if requests.Load() != 0 {
				t.Fatal("client construction contacted the server")
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			got, err := client.ListSIPInboundTrunk(ctx, &livekit.ListSIPInboundTrunkRequest{})
			if err != nil {
				t.Fatalf("list inbound trunks: %v", err)
			}
			if !proto.Equal(got, want) {
				t.Fatalf("unexpected trunks: got %v, want %v", got, want)
			}
		})
	}
}
