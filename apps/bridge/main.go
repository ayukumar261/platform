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

	"bridge/internal/agents/providers/cartesia"
	"bridge/internal/bridge"
	"bridge/internal/clients"
	"bridge/internal/inbound"
	"bridge/internal/outbound"
	"bridge/internal/telephony"
	"bridge/internal/telephony/livekit"

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
	BridgeAPIToken          string
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
	bridge := bridge.New(
		livekit.New(configuration.LiveKitURL, configuration.LiveKitAPIKey, configuration.LiveKitAPISecret),
		cartesia.New(configuration.CartesiaAPIKey),
		configuration.CartesiaInboundAgentID,
		configuration.CartesiaOutboundAgentID,
		trunkIDs,
	)

	// Run until Docker asks the bridge to stop
	running, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Answer each inbound call in the background
	var answering sync.WaitGroup
	answer := func(request telephony.InboundRequest) {
		answering.Go(func() {
			log.Printf("Answering the call in room %s", request.RoomName)
			if err := bridge.Answer(running, request); err != nil {
				log.Printf("The call in room %s failed: %v", request.RoomName, err)
				return
			}
			log.Printf("The call in room %s ended", request.RoomName)
		})
	}

	// Start accepted outbound calls with the bridge's lifetime
	outgoing := outbound.NewHandler(running, configuration.BridgeAPIToken, func(ctx context.Context, request outbound.Request) error {
		return bridge.Dial(ctx, "twilio", request)
	})

	// Listen for inbound webhooks and outbound requests
	keys := auth.NewSimpleKeyProvider(configuration.LiveKitAPIKey, configuration.LiveKitAPISecret)
	routes := http.NewServeMux()
	routes.Handle("POST /livekit/webhook", inbound.Webhook(keys, answer))
	routes.Handle("POST /call", outgoing)
	server := &http.Server{
		Handler:           routes,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("", configuration.BridgePort))
	if err != nil {
		log.Fatalf("Listen for HTTP requests failed: %v", err)
	}
	go func() {
		if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Serve HTTP requests failed: %v", err)
		}
	}()
	log.Printf("Listening for HTTP requests on %s", listener.Addr())

	// Wait for the stop signal, then let calls in progress hang up
	<-running.Done()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	if err := server.Shutdown(ctx); err != nil {
		server.Close()
	}
	cancel()
	outgoing.Shutdown()
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
		BridgeAPIToken:          required("BRIDGE_API_TOKEN"),
	}
	if strings.ContainsAny(configuration.BridgeAPIToken, " \t\r\n") {
		invalid = append(invalid, "BRIDGE_API_TOKEN")
	}

	// Report all missing variables at once
	if len(invalid) > 0 {
		return Configuration{}, fmt.Errorf("required environment variables are invalid: %s", strings.Join(invalid, ", "))
	}

	return configuration, nil
}
