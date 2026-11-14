package inbound

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

// Constants
const testTrunkID = "ST_test"

type fakeLiveKit struct {
	t             *testing.T
	trunks        []*livekit.SIPInboundTrunkInfo
	dispatchRules []*livekit.SIPDispatchRuleInfo
	calls         []string
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
	case "ListSIPInboundTrunk":
		response = &livekit.ListSIPInboundTrunkResponse{Items: f.trunks}
	case "CreateSIPInboundTrunk":
		request := &livekit.CreateSIPInboundTrunkRequest{}
		if err := proto.Unmarshal(body, request); err != nil {
			f.t.Fatal(err)
		}
		request.Trunk.SipTrunkId = testTrunkID
		f.trunks = append(f.trunks, request.Trunk)
		response = request.Trunk
	case "ListSIPDispatchRule":
		response = &livekit.ListSIPDispatchRuleResponse{Items: f.dispatchRules}
	case "CreateSIPDispatchRule":
		request := &livekit.CreateSIPDispatchRuleRequest{}
		if err := proto.Unmarshal(body, request); err != nil {
			f.t.Fatal(err)
		}
		f.dispatchRules = append(f.dispatchRules, request.DispatchRule)
		response = request.DispatchRule
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

// existingTrunks returns the inbound trunk.
func existingTrunks() []*livekit.SIPInboundTrunkInfo {
	return []*livekit.SIPInboundTrunkInfo{{SipTrunkId: testTrunkID, Name: trunkName}}
}

func setup(t *testing.T, fake *fakeLiveKit) error {
	t.Helper()
	fake.t = t
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	sipClient := lksdk.NewSIPClient(server.URL, "test-api-key", "test-api-secret")
	return Setup(context.Background(), sipClient)
}

func TestSetupCreatesTrunkAndDispatchRule(t *testing.T) {
	fake := &fakeLiveKit{}
	if err := setup(t, fake); err != nil {
		t.Fatal(err)
	}

	if len(fake.trunks) != 1 {
		t.Fatalf("expected one trunk, got %d", len(fake.trunks))
	}
	trunk := fake.trunks[0]
	if trunk.Name != trunkName || !slices.Equal(trunk.Numbers, []string{phoneNumber}) {
		t.Errorf("unexpected trunk: %v", trunk)
	}

	if len(fake.dispatchRules) != 1 {
		t.Fatalf("expected one dispatch rule, got %d", len(fake.dispatchRules))
	}
	rule := fake.dispatchRules[0]
	if rule.Name != dispatchRuleName || !slices.Equal(rule.TrunkIds, []string{testTrunkID}) {
		t.Errorf("unexpected dispatch rule: %v", rule)
	}
	if prefix := rule.Rule.GetDispatchRuleIndividual().GetRoomPrefix(); prefix != roomPrefix {
		t.Errorf("expected room prefix %q, got %q", roomPrefix, prefix)
	}
}

func TestSetupKeepsExistingTrunkAndDispatchRule(t *testing.T) {
	fake := &fakeLiveKit{
		trunks:        existingTrunks(),
		dispatchRules: []*livekit.SIPDispatchRuleInfo{{Name: dispatchRuleName}},
	}
	if err := setup(t, fake); err != nil {
		t.Fatal(err)
	}

	expected := []string{"ListSIPInboundTrunk", "ListSIPDispatchRule"}
	if !slices.Equal(fake.calls, expected) {
		t.Errorf("expected calls %v, got %v", expected, fake.calls)
	}
}

func TestSetupReportsFailingStep(t *testing.T) {
	// Close the server first, so that the very first request fails
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()

	sipClient := lksdk.NewSIPClient(server.URL, "test-api-key", "test-api-secret")
	err := Setup(context.Background(), sipClient)
	if err == nil || !strings.Contains(err.Error(), "ensure inbound trunk") {
		t.Fatalf("expected an inbound trunk error, got %v", err)
	}
}
