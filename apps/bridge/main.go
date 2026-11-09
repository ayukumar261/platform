package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Configuration struct {
	LiveKitURL       string
	LiveKitAPIKey    string
	LiveKitAPISecret string
}

func main() {
	if _, err := loadConfiguration("../../.env"); err != nil {
		log.Fatal(err)
	}
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
