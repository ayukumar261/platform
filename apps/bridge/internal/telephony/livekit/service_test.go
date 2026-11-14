package livekit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"bridge/internal/telephony"

	"github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

// Constants
const (
	testCallID   = "SCL_test"
	testRoomName = "call-test"
	testPhone    = "sip-caller"
)

type fakeLiveKit struct {
	t           *testing.T
	participant *livekit.ParticipantInfo
	dialFails   bool
	phoneGone   bool
	dialed      []*livekit.CreateSIPParticipantRequest
	removed     []*livekit.RoomParticipantIdentity
}

func (f *fakeLiveKit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Fatal(err)
	}

	var response proto.Message
	switch r.URL.Path {
	case "/twirp/livekit.SIP/CreateSIPParticipant":
		request := &livekit.CreateSIPParticipantRequest{}
		if err := proto.Unmarshal(body, request); err != nil {
			f.t.Fatal(err)
		}
		f.dialed = append(f.dialed, request)
		if f.dialFails {
			twirpError(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		response = &livekit.SIPParticipantInfo{SipCallId: testCallID}
	case "/twirp/livekit.RoomService/GetParticipant":
		response = f.participant
	case "/twirp/livekit.RoomService/RemoveParticipant":
		request := &livekit.RoomParticipantIdentity{}
		if err := proto.Unmarshal(body, request); err != nil {
			f.t.Fatal(err)
		}
		f.removed = append(f.removed, request)
		if f.phoneGone {
			twirpError(w, http.StatusNotFound, "not_found")
			return
		}
		response = &livekit.RemoveParticipantResponse{}
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

// twirpError replies with a Twirp error, as LiveKit does.
func twirpError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, `{"code":"`+code+`","msg":"test error"}`)
}

type fakeMedia struct {
	readyErr error
	codes    []uint32
	digits   []string
	done     chan struct{}
}

func (m *fakeMedia) ReadAudio(context.Context) ([]byte, error) { return nil, io.EOF }
func (m *fakeMedia) WriteAudio(context.Context, []byte) error  { return nil }
func (m *fakeMedia) ClearAudio(context.Context) error          { return nil }
func (m *fakeMedia) DrainAudio(context.Context) error          { return nil }
func (m *fakeMedia) WaitReady(context.Context) error           { return m.readyErr }
func (m *fakeMedia) Done() <-chan struct{}                     { return m.done }
func (m *fakeMedia) Err() error                                { return nil }

func (m *fakeMedia) PressKey(code uint32, digit string) error {
	m.codes = append(m.codes, code)
	m.digits = append(m.digits, digit)
	return nil
}

func (m *fakeMedia) Close() {
	if !m.closed() {
		close(m.done)
	}
}

// closed reports whether the service left the room.
func (m *fakeMedia) closed() bool {
	select {
	case <-m.done:
		return true
	default:
		return false
	}
}

// phoneParticipant returns the participant that LiveKit SIP creates for a caller.
func phoneParticipant() *livekit.ParticipantInfo {
	return &livekit.ParticipantInfo{
		Identity: testPhone,
		Kind:     livekit.ParticipantInfo_SIP,
		Attributes: map[string]string{
			livekit.AttrSIPCallID:      testCallID,
			livekit.AttrSIPPhoneNumber: "+15550000001",
			livekit.AttrSIPTrunkNumber: "+15550000002",
		},
	}
}

// testService returns a service that uses fake for requests and media for rooms.
func testService(t *testing.T, fake *fakeLiveKit, media *fakeMedia) *Service {
	t.Helper()
	fake.t = t
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	media.done = make(chan struct{})
	service := New(server.URL, "test-api-key", "test-api-secret")
	service.join = func(context.Context, string, string) (transport, error) {
		return media, nil
	}
	return service
}

// dial places a test call to +15550000003.
func dial(service *Service) (telephony.Call, error) {
	return service.DialOutbound(context.Background(), telephony.OutboundRequest{
		TrunkID:  "ST_test",
		From:     "+15550000001",
		To:       "+15550000003",
		RoomName: testRoomName,
	})
}

func TestDialOutboundPlacesCall(t *testing.T) {
	fake := &fakeLiveKit{}
	call, err := dial(testService(t, fake, &fakeMedia{}))
	if err != nil {
		t.Fatal(err)
	}

	if len(fake.dialed) != 1 {
		t.Fatalf("expected one call, got %d", len(fake.dialed))
	}
	request := fake.dialed[0]
	if request.SipTrunkId != "ST_test" || request.SipNumber != "+15550000001" || request.SipCallTo != "+15550000003" {
		t.Errorf("unexpected trunk or numbers: %v", request)
	}
	if request.RoomName != testRoomName || request.ParticipantIdentity != call.Info().ParticipantIdentity {
		t.Errorf("unexpected room or identity: %v", request)
	}
	if !request.WaitUntilAnswered {
		t.Error("expected to wait until the call is answered")
	}

	info := call.Info()
	if info.ID != testCallID || info.Direction != telephony.Outbound {
		t.Errorf("unexpected call info: %+v", info)
	}
}

func TestDialOutboundHangsUpWhenDialFails(t *testing.T) {
	fake := &fakeLiveKit{dialFails: true}
	media := &fakeMedia{}
	if _, err := dial(testService(t, fake, media)); err == nil {
		t.Fatal("expected a dial error")
	}

	if len(fake.removed) != 1 || fake.removed[0].Identity != fake.dialed[0].ParticipantIdentity {
		t.Errorf("expected the phone to be removed, got %v", fake.removed)
	}
	if !media.closed() {
		t.Error("expected the agent to leave the room")
	}
}

func TestDialOutboundHangsUpWhenAudioFails(t *testing.T) {
	fake := &fakeLiveKit{}
	media := &fakeMedia{readyErr: errors.New("no audio")}
	if _, err := dial(testService(t, fake, media)); err == nil {
		t.Fatal("expected an audio error")
	}

	if len(fake.removed) != 1 {
		t.Errorf("expected the phone to be removed, got %v", fake.removed)
	}
	if !media.closed() {
		t.Error("expected the agent to leave the room")
	}
}

func TestAttachInboundReadsCallNumbers(t *testing.T) {
	fake := &fakeLiveKit{participant: phoneParticipant()}
	service := testService(t, fake, &fakeMedia{})

	call, err := service.AttachInbound(context.Background(), telephony.InboundRequest{RoomName: testRoomName, ParticipantIdentity: testPhone})
	if err != nil {
		t.Fatal(err)
	}

	expected := telephony.Info{
		ID:                  testCallID,
		Direction:           telephony.Inbound,
		From:                "+15550000001",
		To:                  "+15550000002",
		RoomName:            testRoomName,
		ParticipantIdentity: testPhone,
	}
	if call.Info() != expected {
		t.Errorf("expected %+v, got %+v", expected, call.Info())
	}
	if len(fake.dialed) != 0 {
		t.Error("expected no outbound call")
	}
}

func TestAttachInboundKeepsCallerWhenAudioFails(t *testing.T) {
	fake := &fakeLiveKit{participant: phoneParticipant()}
	media := &fakeMedia{readyErr: errors.New("no audio")}
	service := testService(t, fake, media)

	_, err := service.AttachInbound(context.Background(), telephony.InboundRequest{RoomName: testRoomName, ParticipantIdentity: testPhone})
	if err == nil {
		t.Fatal("expected an audio error")
	}

	if len(fake.removed) != 0 {
		t.Errorf("expected the caller to stay on the line, got %v", fake.removed)
	}
	if !media.closed() {
		t.Error("expected the agent to leave the room")
	}
}

func TestAttachInboundRejectsNonPhoneParticipant(t *testing.T) {
	participant := phoneParticipant()
	participant.Kind = livekit.ParticipantInfo_STANDARD
	service := testService(t, &fakeLiveKit{participant: participant}, &fakeMedia{})

	_, err := service.AttachInbound(context.Background(), telephony.InboundRequest{RoomName: testRoomName, ParticipantIdentity: testPhone})
	if err == nil || !strings.Contains(err.Error(), "not a phone call") {
		t.Fatalf("expected a not-a-phone error, got %v", err)
	}
}

func TestSendDTMFPressesKeys(t *testing.T) {
	media := &fakeMedia{}
	call, err := dial(testService(t, &fakeLiveKit{}, media))
	if err != nil {
		t.Fatal(err)
	}

	if err := call.SendDTMF(context.Background(), "09*#ABCD"); err != nil {
		t.Fatal(err)
	}

	if expected := []uint32{0, 9, 10, 11, 12, 13, 14, 15}; !slices.Equal(media.codes, expected) {
		t.Errorf("expected codes %v, got %v", expected, media.codes)
	}
	if expected := []string{"0", "9", "*", "#", "A", "B", "C", "D"}; !slices.Equal(media.digits, expected) {
		t.Errorf("expected digits %v, got %v", expected, media.digits)
	}
}

func TestSendDTMFRejectsInvalidKeyBeforeSending(t *testing.T) {
	media := &fakeMedia{}
	call, err := dial(testService(t, &fakeLiveKit{}, media))
	if err != nil {
		t.Fatal(err)
	}

	if err := call.SendDTMF(context.Background(), "12x"); err == nil {
		t.Fatal("expected an error for an invalid key")
	}
	if len(media.digits) != 0 {
		t.Errorf("expected no keys to be pressed, got %v", media.digits)
	}
}

func TestEndRemovesPhoneAndLeavesRoom(t *testing.T) {
	fake := &fakeLiveKit{}
	media := &fakeMedia{}
	call, err := dial(testService(t, fake, media))
	if err != nil {
		t.Fatal(err)
	}

	if err := call.End(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(fake.removed) != 1 {
		t.Fatalf("expected one removal, got %v", fake.removed)
	}
	if removed := fake.removed[0]; removed.Room != testRoomName || removed.Identity != call.Info().ParticipantIdentity {
		t.Errorf("expected only the phone to be removed, got %v", removed)
	}
	if !media.closed() {
		t.Error("expected the agent to leave the room")
	}
}

func TestEndSucceedsWhenPhoneAlreadyLeft(t *testing.T) {
	fake := &fakeLiveKit{}
	call, err := dial(testService(t, fake, &fakeMedia{}))
	if err != nil {
		t.Fatal(err)
	}

	fake.phoneGone = true
	if err := call.End(context.Background()); err != nil {
		t.Fatalf("expected a hung-up call to end without error, got %v", err)
	}
}
