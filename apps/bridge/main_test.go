package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var liveKitVariables = []string{"LIVEKIT_URL", "LIVEKIT_API_KEY", "LIVEKIT_API_SECRET"}

var testConfiguration = Configuration{
	LiveKitURL:       "ws://127.0.0.1:7880",
	LiveKitAPIKey:    "test-api-key",
	LiveKitAPISecret: "test-api-secret",
}

func setTestEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("LIVEKIT_URL", testConfiguration.LiveKitURL)
	t.Setenv("LIVEKIT_API_KEY", testConfiguration.LiveKitAPIKey)
	t.Setenv("LIVEKIT_API_SECRET", testConfiguration.LiveKitAPISecret)
}

func clearTestEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range liveKitVariables {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadConfigurationFromEnvironment(t *testing.T) {
	setTestEnvironment(t)
	configuration, err := loadConfiguration("")
	if err != nil {
		t.Fatal(err)
	}
	if configuration != testConfiguration {
		t.Fatal("configuration does not match the environment")
	}
}

func TestLoadConfigurationWithoutEnvironmentFile(t *testing.T) {
	setTestEnvironment(t)
	configuration, err := loadConfiguration(filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if configuration != testConfiguration {
		t.Fatal("configuration does not match the environment")
	}
}

func TestLoadConfigurationRequiresEveryVariable(t *testing.T) {
	for _, name := range liveKitVariables {
		for _, state := range []string{"unset", "empty", "whitespace"} {
			t.Run(name+"/"+state, func(t *testing.T) {
				setTestEnvironment(t)
				switch state {
				case "unset":
					if err := os.Unsetenv(name); err != nil {
						t.Fatal(err)
					}
				case "empty":
					t.Setenv(name, "")
				case "whitespace":
					t.Setenv(name, " \t\n")
				}

				configuration, err := loadConfiguration("")
				if err == nil || !strings.Contains(err.Error(), name) {
					t.Fatalf("expected an error naming %s, got %v", name, err)
				}
				if configuration != (Configuration{}) {
					t.Fatal("expected no configuration on failure")
				}
			})
		}
	}
}

func TestLoadConfigurationReportsAllMissingVariables(t *testing.T) {
	clearTestEnvironment(t)
	_, err := loadConfiguration("")
	if err == nil {
		t.Fatal("expected an error for missing configuration")
	}
	for _, name := range liveKitVariables {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not name %s", name)
		}
	}
}

func TestLoadConfigurationFromFile(t *testing.T) {
	for _, override := range []string{"none", "environment", "empty"} {
		t.Run(override, func(t *testing.T) {
			clearTestEnvironment(t)
			environmentFile := filepath.Join(t.TempDir(), ".env")
			contents := "LIVEKIT_URL=ws://127.0.0.1:7880\nLIVEKIT_API_KEY=test-api-key\nLIVEKIT_API_SECRET=test-api-secret\n"
			if err := os.WriteFile(environmentFile, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}

			expected := testConfiguration
			switch override {
			case "environment":
				expected.LiveKitAPIKey = "override-api-key"
				t.Setenv("LIVEKIT_API_KEY", expected.LiveKitAPIKey)
			case "empty":
				t.Setenv("LIVEKIT_API_KEY", "")
			}

			configuration, err := loadConfiguration(environmentFile)
			if override == "empty" {
				if err == nil || !strings.Contains(err.Error(), "LIVEKIT_API_KEY") {
					t.Fatalf("expected empty environment override to fail, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if configuration != expected {
				t.Fatal("configuration does not match the file and environment overrides")
			}
		})
	}
}

func TestLoadConfigurationRejectsInvalidFiles(t *testing.T) {
	for _, state := range []string{"directory", "malformed"} {
		t.Run(state, func(t *testing.T) {
			setTestEnvironment(t)
			environmentFile := filepath.Join(t.TempDir(), ".env")
			if state == "directory" {
				if err := os.Mkdir(environmentFile, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(environmentFile, []byte("LIVEKIT_URL=\"unterminated\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := loadConfiguration(environmentFile); err == nil {
				t.Fatal("expected an error for an invalid environment file")
			}
		})
	}
}

func TestMainExitsOnMissingConfiguration(t *testing.T) {
	if os.Getenv("BRIDGE_TEST_MAIN") == "1" {
		main()
		return
	}

	clearTestEnvironment(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestMainExitsOnMissingConfiguration$")
	command.Dir = filepath.Join(t.TempDir(), "apps", "bridge")
	if err := os.MkdirAll(command.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	command.Env = append(os.Environ(), "BRIDGE_TEST_MAIN=1")
	output, err := command.CombinedOutput()
	if exitError, ok := err.(*exec.ExitError); !ok || exitError.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %v", err)
	}
	for _, name := range liveKitVariables {
		if !strings.Contains(string(output), name) {
			t.Errorf("startup error does not name %s", name)
		}
	}
}
