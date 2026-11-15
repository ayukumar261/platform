package bridge

import (
	"context"
	"errors"
	"testing"

	"bridge/internal/agents"
	"bridge/internal/outbound"
	"bridge/internal/relay"
	"bridge/internal/telephony"
)

func TestDialUsesTwilioAndPreservesAcceptedCallID(t *testing.T) {
	failed := errors.New("dial failed")
	dials := 0
	calls := testCalls{
		dial: func(_ context.Context, request telephony.OutboundRequest) (telephony.Call, error) {
			dials++
			expected := telephony.OutboundRequest{TrunkID: "ST_twilio", To: "+15551234567", RoomName: "outbound-test-call"}
			if request != expected {
				t.Fatalf("unexpected outbound request: %+v", request)
			}
			return nil, failed
		},
	}
	bridge := New(calls, nil, "inbound-agent", "outbound-agent", map[string]string{"twilio": "ST_twilio"})
	request := outbound.Request{CallID: "test-call", To: "+15551234567"}
	if err := bridge.Dial(t.Context(), "twilio", request); !errors.Is(err, failed) {
		t.Fatalf("expected the dial failure, got %v", err)
	}
	if dials != 1 {
		t.Fatalf("expected one dial, got %d", dials)
	}
}

func TestDialRejectsUnknownCarrierBeforeDialing(t *testing.T) {
	calls := testCalls{
		dial: func(context.Context, telephony.OutboundRequest) (telephony.Call, error) {
			t.Fatal("an unknown carrier must not start a call")
			return nil, nil
		},
	}
	bridge := New(calls, nil, "inbound-agent", "outbound-agent", map[string]string{"twilio": "ST_twilio"})
	if err := bridge.Dial(t.Context(), "unknown", outbound.Request{}); err == nil {
		t.Fatal("expected an error for an unknown carrier")
	}
}

func TestCallsUseMatchingAgentAndHangUpIfAgentSetupFails(t *testing.T) {
	for _, direction := range []string{"inbound", "outbound"} {
		t.Run(direction, func(t *testing.T) {
			failed := errors.New("agent setup failed")
			ended := false
			call := testCall{end: func(context.Context) error {
				ended = true
				return nil
			}}
			incoming := telephony.InboundRequest{RoomName: "incoming-call", ParticipantIdentity: "caller"}
			requests := 0
			calls := testCalls{
				attach: func(_ context.Context, request telephony.InboundRequest) (telephony.Call, error) {
					requests++
					if direction != "inbound" || request != incoming {
						t.Fatalf("unexpected inbound request: %+v", request)
					}
					return call, nil
				},
				dial: func(context.Context, telephony.OutboundRequest) (telephony.Call, error) {
					requests++
					if direction != "outbound" {
						t.Fatal("an inbound call must not dial out")
					}
					return call, nil
				},
			}

			// Check the selected agent as the bridge requests its session
			provider := testAgent(func(_ context.Context, config agents.SessionConfig) (agents.Session, error) {
				if config.AgentID != direction+"-agent" || config.Audio != relay.AudioFormat {
					t.Fatalf("unexpected agent configuration: %+v", config)
				}
				return nil, failed
			})
			bridge := New(calls, provider, "inbound-agent", "outbound-agent", map[string]string{"twilio": "ST_twilio"})
			var err error
			if direction == "inbound" {
				err = bridge.Answer(t.Context(), incoming)
			} else {
				err = bridge.Dial(t.Context(), "twilio", outbound.Request{CallID: "test-call", To: "+15551234567"})
			}
			if !errors.Is(err, failed) || !ended {
				t.Fatalf("expected an agent failure and hangup, got %v, ended=%v", err, ended)
			}
			if requests != 1 {
				t.Fatalf("expected one call request, got %d", requests)
			}
		})
	}
}

type testCalls struct {
	dial   func(context.Context, telephony.OutboundRequest) (telephony.Call, error)
	attach func(context.Context, telephony.InboundRequest) (telephony.Call, error)
}

func (s testCalls) DialOutbound(ctx context.Context, request telephony.OutboundRequest) (telephony.Call, error) {
	return s.dial(ctx, request)
}

func (s testCalls) AttachInbound(ctx context.Context, request telephony.InboundRequest) (telephony.Call, error) {
	return s.attach(ctx, request)
}

type testAgent func(context.Context, agents.SessionConfig) (agents.Session, error)

func (testAgent) Name() string { return "test" }

func (f testAgent) NewSession(ctx context.Context, config agents.SessionConfig) (agents.Session, error) {
	return f(ctx, config)
}

type testCall struct {
	telephony.Call
	end func(context.Context) error
}

func (c testCall) End(ctx context.Context) error {
	return c.end(ctx)
}
