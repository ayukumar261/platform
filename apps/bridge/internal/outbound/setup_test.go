package outbound

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"google.golang.org/protobuf/proto"
)

const (
	testTrunkID         = "ST_test"
	testKamailioAddress = "kamailio:5060"
)

// fakeLiveKit serves the LiveKit SIP API from memory and records the requests it receives.
type fakeLiveKit struct {
	t      *testing.T
	trunks []*livekit.SIPOutboundTrunkInfo
	calls  []string
}

func (f *fakeLiveKit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/twirp/livekit.SIP/")
	f.calls = append(f.calls, method)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Fatal(err)
	}

	var response proto.Message
	switch method {
	case "ListSIPOutboundTrunk":
		response = &livekit.ListSIPOutboundTrunkResponse{Items: f.trunks}
	case "CreateSIPOutboundTrunk":
		request := &livekit.CreateSIPOutboundTrunkRequest{}
		if err := proto.Unmarshal(body, request); err != nil {
			f.t.Fatal(err)
		}
		request.Trunk.SipTrunkId = testTrunkID
		f.trunks = append(f.trunks, request.Trunk)
		response = request.Trunk
	default:
		http.NotFound(w, r)
		return
	}

	encoded, err := proto.Marshal(response)
	if err != nil {
		f.t.Fatal(err)
	}
	w.Header().Set("Content-Type", "application/protobuf")
	if _, err := w.Write(encoded); err != nil {
		f.t.Errorf("write response: %v", err)
	}
}

func setup(t *testing.T, fake *fakeLiveKit) (string, error) {
	t.Helper()
	fake.t = t
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	sipClient := lksdk.NewSIPClient(server.URL, "test-api-key", "test-api-secret")
	return Setup(context.Background(), sipClient, testKamailioAddress)
}

func TestSetupCreatesTrunk(t *testing.T) {
	fake := &fakeLiveKit{}
	trunkID, err := setup(t, fake)
	if err != nil {
		t.Fatal(err)
	}
	if trunkID != testTrunkID {
		t.Errorf("expected trunk ID %q, got %q", testTrunkID, trunkID)
	}

	if len(fake.trunks) != 1 {
		t.Fatalf("expected one trunk, got %d", len(fake.trunks))
	}
	trunk := fake.trunks[0]
	if trunk.Name != trunkName || trunk.Address != testKamailioAddress || !slices.Equal(trunk.Numbers, []string{phoneNumber}) {
		t.Errorf("unexpected trunk: %v", trunk)
	}
}

func TestSetupKeepsExistingTrunk(t *testing.T) {
	fake := &fakeLiveKit{
		trunks: []*livekit.SIPOutboundTrunkInfo{{SipTrunkId: testTrunkID, Name: trunkName}},
	}
	trunkID, err := setup(t, fake)
	if err != nil {
		t.Fatal(err)
	}
	if trunkID != testTrunkID {
		t.Errorf("expected trunk ID %q, got %q", testTrunkID, trunkID)
	}

	expected := []string{"ListSIPOutboundTrunk"}
	if !slices.Equal(fake.calls, expected) {
		t.Errorf("expected calls %v, got %v", expected, fake.calls)
	}
}

func TestSetupReportsFailingStep(t *testing.T) {
	// Closing the server first makes the very first request fail.
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()

	sipClient := lksdk.NewSIPClient(server.URL, "test-api-key", "test-api-secret")
	trunkID, err := Setup(context.Background(), sipClient, testKamailioAddress)
	if err == nil || !strings.Contains(err.Error(), "ensure outbound trunk") {
		t.Fatalf("expected an outbound trunk error, got %v", err)
	}
	if trunkID != "" {
		t.Errorf("expected no trunk ID on failure, got %q", trunkID)
	}
}
