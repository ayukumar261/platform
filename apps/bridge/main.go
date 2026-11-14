package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"bridge/internal/agents"
	"bridge/internal/agents/providers/cartesia"
	"bridge/internal/clients"
	"bridge/internal/inbound"
	"bridge/internal/outbound"
	"bridge/internal/relay"
	"bridge/internal/telephony"
	"bridge/internal/telephony/livekit"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/livekit/protocol/auth"
)

type Configuration struct {
	LiveKitURL              string
	LiveKitAPIKey           string
	LiveKitAPISecret        string
	KamailioHost            string
	KamailioPort            string
	CartesiaAPIKey          string
	CartesiaInboundAgentID  string
	CartesiaOutboundAgentID string
	BridgePort              string
}

func main() {
	// Read the settings from the environment
	configuration, err := loadConfiguration("../../.env")
	if err != nil {
		log.Fatal(err)
	}

	// Create the client that talks to LiveKit
	sipClient := clients.NewLiveKitSIPClient(
		configuration.LiveKitURL,
		configuration.LiveKitAPIKey,
		configuration.LiveKitAPISecret,
	)

	// Prepare LiveKit for inbound calls
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = inbound.Setup(ctx, sipClient)
	cancel()
	if err != nil {
		log.Fatalf("LiveKit inbound setup failed: %v", err)
	}
	log.Print("LiveKit inbound setup succeeded")

	// Prepare LiveKit for outbound calls
	kamailioAddress := net.JoinHostPort(configuration.KamailioHost, configuration.KamailioPort)
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	trunkIDs, err := outbound.Setup(ctx, sipClient, kamailioAddress)
	cancel()
	if err != nil {
		log.Fatalf("LiveKit outbound setup failed: %v", err)
	}
	log.Print("LiveKit outbound setup succeeded")

	// Connect calls to the Cartesia agents
	bridge := &agentBridge{
		calls:           livekit.New(configuration.LiveKitURL, configuration.LiveKitAPIKey, configuration.LiveKitAPISecret),
		agents:          cartesia.New(configuration.CartesiaAPIKey),
		inboundAgentID:  configuration.CartesiaInboundAgentID,
		outboundAgentID: configuration.CartesiaOutboundAgentID,
		trunkIDs:        trunkIDs,
	}

	// Run until Docker asks the bridge to stop
	running, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Answer each inbound call in the background
	var answering sync.WaitGroup
	answer := func(request telephony.InboundRequest) {
		answering.Go(func() {
			log.Printf("Answering the call in room %s", request.RoomName)
			if err := bridge.answer(running, request); err != nil {
				log.Printf("The call in room %s failed: %v", request.RoomName, err)
				return
			}
			log.Printf("The call in room %s ended", request.RoomName)
		})
	}

	// Listen for LiveKit's webhooks, which report inbound calls
	keys := auth.NewSimpleKeyProvider(configuration.LiveKitAPIKey, configuration.LiveKitAPISecret)
	routes := http.NewServeMux()
	routes.Handle("POST /livekit/webhook", inbound.Webhook(keys, answer))
	server := &http.Server{Handler: routes}
	listener, err := net.Listen("tcp", net.JoinHostPort("", configuration.BridgePort))
	if err != nil {
		log.Fatalf("Listen for LiveKit webhooks failed: %v", err)
	}
	go func() {
		if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Serve LiveKit webhooks failed: %v", err)
		}
	}()
	log.Printf("Listening for LiveKit webhooks on %s", listener.Addr())

	// Wait for the stop signal, then let calls in progress hang up
	<-running.Done()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	server.Shutdown(ctx)
	cancel()
	answering.Wait()
	log.Print("Bridge stopped")
}

func loadConfiguration(environmentFile string) (Configuration, error) {
	// Load the environment file if there is one
	if environmentFile != "" {
		if err := godotenv.Load(environmentFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Configuration{}, fmt.Errorf("load environment file %q: %w", environmentFile, err)
		}
	}

	// Remember the name of every missing variable
	var invalid []string
	required := func(name string) string {
		value := os.Getenv(name)
		if strings.TrimSpace(value) == "" {
			invalid = append(invalid, name)
		}
		return value
	}

	// Read every required variable
	configuration := Configuration{
		LiveKitURL:              required("LIVEKIT_URL"),
		LiveKitAPIKey:           required("LIVEKIT_API_KEY"),
		LiveKitAPISecret:        required("LIVEKIT_API_SECRET"),
		KamailioHost:            required("KAMAILIO_HOST"),
		KamailioPort:            required("KAMAILIO_PORT"),
		CartesiaAPIKey:          required("CARTESIA_API_KEY"),
		CartesiaInboundAgentID:  required("CARTESIA_INBOUND_AGENT_ID"),
		CartesiaOutboundAgentID: required("CARTESIA_OUTBOUND_AGENT_ID"),
		BridgePort:              required("BRIDGE_PORT"),
	}

	// Report all missing variables at once
	if len(invalid) > 0 {
		return Configuration{}, fmt.Errorf("required environment variables are invalid: %s", strings.Join(invalid, ", "))
	}

	return configuration, nil
}

type agentBridge struct {
	calls           telephony.Service
	agents          agents.Provider
	inboundAgentID  string
	outboundAgentID string
	trunkIDs        map[string]string
}

// answer connects the inbound agent to a call, and returns when the call ends.
//
// Parameters:
//   - ctx: hangs up early if it is cancelled
//   - request: the room and the phone participant that LiveKit reported
//
// Returns:
//   - nil when the call ends normally
//   - an error if the call cannot be answered or fails
func (b *agentBridge) answer(ctx context.Context, request telephony.InboundRequest) error {
	// Join the call, giving up if it takes too long
	setup, cancel := context.WithTimeout(ctx, 10*time.Second)
	call, err := b.calls.AttachInbound(setup, request)
	cancel()
	if err != nil {
		return fmt.Errorf("attach inbound call: %w", err)
	}

	return b.talk(ctx, call, b.inboundAgentID)
}

// dial calls a number through a carrier, connects the outbound agent, and
// returns when the call ends. Nothing calls it yet.
//
// Parameters:
//   - ctx: hangs up early if it is cancelled
//   - carrier: the carrier to call through, such as "telnyx"
//   - to: the number to call, such as "+15550000000"
//
// Returns:
//   - nil when the call ends normally
//   - an error if nobody answers or the call fails
func (b *agentBridge) dial(ctx context.Context, carrier, to string) error {
	trunkID, ok := b.trunkIDs[carrier]
	if !ok {
		return fmt.Errorf("unknown carrier %q", carrier)
	}

	// Call the number from a new room, giving up if nobody answers in time
	setup, cancel := context.WithTimeout(ctx, time.Minute)
	call, err := b.calls.DialOutbound(setup, telephony.OutboundRequest{
		TrunkID:  trunkID,
		To:       to,
		RoomName: "outbound-" + uuid.NewString(),
	})
	cancel()
	if err != nil {
		return fmt.Errorf("dial outbound call: %w", err)
	}

	return b.talk(ctx, call, b.outboundAgentID)
}

// talk starts an agent session on a call, and returns when the call ends.
//
// Parameters:
//   - ctx: hangs up early if it is cancelled
//   - call: the answered phone call
//   - agentID: the Cartesia agent that talks on the call
//
// Returns:
//   - nil when the call ends normally
//   - an error if the session cannot start or the call fails
func (b *agentBridge) talk(ctx context.Context, call telephony.Call, agentID string) error {
	session, err := b.agents.NewSession(ctx, agents.SessionConfig{AgentID: agentID, Audio: relay.AudioFormat})
	if err != nil {
		return errors.Join(fmt.Errorf("create agent session: %w", err), call.End(ctx))
	}

	return relay.Connect(ctx, call, session, nil)
}
