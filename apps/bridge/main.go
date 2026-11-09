package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"bridge/internal/clients"

	"github.com/joho/godotenv"
	"github.com/livekit/protocol/livekit"
)

type Configuration struct {
	LiveKitURL       string
	LiveKitAPIKey    string
	LiveKitAPISecret string
}

func main() {
	configuration, err := loadConfiguration("../../.env")
	if err != nil {
		log.Fatal(err)
	}

	sipClient := clients.NewLiveKitSIPClient(
		configuration.LiveKitURL,
		configuration.LiveKitAPIKey,
		configuration.LiveKitAPISecret,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = sipClient.ListSIPInboundTrunk(ctx, &livekit.ListSIPInboundTrunkRequest{})
	cancel()
	if err != nil {
		log.Fatalf("LiveKit SIP startup check failed: %v", err)
	}
	log.Print("LiveKit SIP startup check succeeded")
}

func loadConfiguration(environmentFile string) (Configuration, error) {
	if environmentFile != "" {
		if err := godotenv.Load(environmentFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Configuration{}, fmt.Errorf("load environment file %q: %w", environmentFile, err)
		}
	}

	var invalid []string
	required := func(name string) string {
		value := os.Getenv(name)
		if strings.TrimSpace(value) == "" {
			invalid = append(invalid, name)
		}
		return value
	}

	configuration := Configuration{
		LiveKitURL:       required("LIVEKIT_URL"),
		LiveKitAPIKey:    required("LIVEKIT_API_KEY"),
		LiveKitAPISecret: required("LIVEKIT_API_SECRET"),
	}
	if len(invalid) > 0 {
		return Configuration{}, fmt.Errorf("required environment variables are invalid: %s", strings.Join(invalid, ", "))
	}

	return configuration, nil
}
