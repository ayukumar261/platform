package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"

	"bridge/internal/clients"
	"bridge/internal/inbound"
	"bridge/internal/outbound"

	"github.com/joho/godotenv"
)

type Configuration struct {
	LiveKitURL       string
	LiveKitAPIKey    string
	LiveKitAPISecret string
	KamailioHost     string
	KamailioPort     string
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
	err = outbound.Setup(ctx, sipClient, kamailioAddress)
	cancel()
	if err != nil {
		log.Fatalf("LiveKit outbound setup failed: %v", err)
	}
	log.Print("LiveKit outbound setup succeeded")
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
		LiveKitURL:       required("LIVEKIT_URL"),
		LiveKitAPIKey:    required("LIVEKIT_API_KEY"),
		LiveKitAPISecret: required("LIVEKIT_API_SECRET"),
		KamailioHost:     required("KAMAILIO_HOST"),
		KamailioPort:     required("KAMAILIO_PORT"),
	}

	// Report all missing variables at once
	if len(invalid) > 0 {
		return Configuration{}, fmt.Errorf("required environment variables are invalid: %s", strings.Join(invalid, ", "))
	}

	return configuration, nil
}
