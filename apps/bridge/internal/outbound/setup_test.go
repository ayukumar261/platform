package outbound

import (
	"context"
	"io"
	"maps"
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
const testKamailioAddress = "kamailio:5070"

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
		request.Trunk.SipTrunkId = "ST_" + request.Trunk.Name
		f.trunks = append(f.trunks, request.Trunk)
		response = request.Trunk
	case "UpdateSIPOutboundTrunk":
		request := &livekit.UpdateSIPOutboundTrunkRequest{}
		if err := proto.Unmarshal(body, request); err != nil {
			f.t.Fatal(err)
		}
		index := slices.IndexFunc(f.trunks, func(trunk *livekit.SIPOutboundTrunkInfo) bool {
			return trunk.SipTrunkId == request.SipTrunkId
		})
		if index < 0 {
			http.NotFound(w, r)
			return
		}
		f.trunks[index].Address = request.GetUpdate().GetAddress()
		response = f.trunks[index]
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

// existingTrunks returns a trunk for every carrier with the given address.
func existingTrunks(address string) []*livekit.SIPOutboundTrunkInfo {
	var trunks []*livekit.SIPOutboundTrunkInfo
	for _, carrier := range carriers {
		trunks = append(trunks, &livekit.SIPOutboundTrunkInfo{
			SipTrunkId: "ST_" + trunkName + "-" + carrier,
			Name:       trunkName + "-" + carrier,
			Address:    address,
		})
	}
	return trunks
}

func setup(t *testing.T, fake *fakeLiveKit) error {
	t.Helper()
	fake.t = t
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	sipClient := lksdk.NewSIPClient(server.URL, "test-api-key", "test-api-secret")
	return Setup(context.Background(), sipClient, testKamailioAddress)
}

func TestSetupCreatesTrunks(t *testing.T) {
	fake := &fakeLiveKit{}
	if err := setup(t, fake); err != nil {
		t.Fatal(err)
	}

	if len(fake.trunks) != len(carriers) {
		t.Fatalf("expected %d trunks, got %d", len(carriers), len(fake.trunks))
	}
	for index, carrier := range carriers {
		trunk := fake.trunks[index]
		if trunk.Name != trunkName+"-"+carrier || trunk.Address != testKamailioAddress || !slices.Equal(trunk.Numbers, []string{phoneNumber}) {
			t.Errorf("unexpected trunk: %v", trunk)
		}
		if !maps.Equal(trunk.Headers, map[string]string{"X-Carrier": carrier}) {
			t.Errorf("unexpected headers for %s: %v", carrier, trunk.Headers)
		}
	}
}

func TestSetupKeepsExistingTrunks(t *testing.T) {
	fake := &fakeLiveKit{trunks: existingTrunks(testKamailioAddress)}
	if err := setup(t, fake); err != nil {
		t.Fatal(err)
	}

	expected := []string{"ListSIPOutboundTrunk", "ListSIPOutboundTrunk"}
	if !slices.Equal(fake.calls, expected) {
		t.Errorf("expected calls %v, got %v", expected, fake.calls)
	}
}

func TestSetupUpdatesTrunkAddresses(t *testing.T) {
	fake := &fakeLiveKit{trunks: existingTrunks("kamailio:5060")}
	if err := setup(t, fake); err != nil {
		t.Fatal(err)
	}

	expected := []string{"ListSIPOutboundTrunk", "UpdateSIPOutboundTrunk", "ListSIPOutboundTrunk", "UpdateSIPOutboundTrunk"}
	if !slices.Equal(fake.calls, expected) {
		t.Errorf("expected calls %v, got %v", expected, fake.calls)
	}
	for _, trunk := range fake.trunks {
		if trunk.Address != testKamailioAddress {
			t.Errorf("expected address %q for %s, got %q", testKamailioAddress, trunk.Name, trunk.Address)
		}
	}
}

func TestSetupReportsFailingStep(t *testing.T) {
	// Closing the server first makes the very first request fail.
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()

	sipClient := lksdk.NewSIPClient(server.URL, "test-api-key", "test-api-secret")
	err := Setup(context.Background(), sipClient, testKamailioAddress)
	if err == nil || !strings.Contains(err.Error(), "ensure outbound trunk for telnyx") {
		t.Fatalf("expected an outbound trunk error, got %v", err)
	}
}
