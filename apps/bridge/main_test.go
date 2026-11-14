package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var requiredVariables = []string{
	"LIVEKIT_URL",
	"LIVEKIT_API_KEY",
	"LIVEKIT_API_SECRET",
	"KAMAILIO_HOST",
	"KAMAILIO_PORT",
	"CARTESIA_API_KEY",
	"CARTESIA_INBOUND_AGENT_ID",
	"CARTESIA_OUTBOUND_AGENT_ID",
	"BRIDGE_PORT",
}

var testConfiguration = Configuration{
	LiveKitURL:              "ws://127.0.0.1:7880",
	LiveKitAPIKey:           "test-api-key",
	LiveKitAPISecret:        "test-api-secret",
	KamailioHost:            "kamailio",
	KamailioPort:            "5070",
	CartesiaAPIKey:          "test-cartesia-key",
	CartesiaInboundAgentID:  "test-inbound-agent",
	CartesiaOutboundAgentID: "test-outbound-agent",
	BridgePort:              "0",
}

func setTestEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("LIVEKIT_URL", testConfiguration.LiveKitURL)
	t.Setenv("LIVEKIT_API_KEY", testConfiguration.LiveKitAPIKey)
	t.Setenv("LIVEKIT_API_SECRET", testConfiguration.LiveKitAPISecret)
	t.Setenv("KAMAILIO_HOST", testConfiguration.KamailioHost)
	t.Setenv("KAMAILIO_PORT", testConfiguration.KamailioPort)
	t.Setenv("CARTESIA_API_KEY", testConfiguration.CartesiaAPIKey)
	t.Setenv("CARTESIA_INBOUND_AGENT_ID", testConfiguration.CartesiaInboundAgentID)
	t.Setenv("CARTESIA_OUTBOUND_AGENT_ID", testConfiguration.CartesiaOutboundAgentID)
	t.Setenv("BRIDGE_PORT", testConfiguration.BridgePort)
}

func clearTestEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range requiredVariables {
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
	for _, name := range requiredVariables {
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
	for _, name := range requiredVariables {
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
			contents := strings.Join([]string{
				"LIVEKIT_URL=ws://127.0.0.1:7880",
				"LIVEKIT_API_KEY=test-api-key",
				"LIVEKIT_API_SECRET=test-api-secret",
				"KAMAILIO_HOST=kamailio",
				"KAMAILIO_PORT=5070",
				"CARTESIA_API_KEY=test-cartesia-key",
				"CARTESIA_INBOUND_AGENT_ID=test-inbound-agent",
				"CARTESIA_OUTBOUND_AGENT_ID=test-outbound-agent",
				"BRIDGE_PORT=0",
			}, "\n")
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
	clearTestEnvironment(t)
	output, err := runBridgeMain(t)
	if exitError, ok := err.(*exec.ExitError); !ok || exitError.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %v", err)
	}
	for _, name := range requiredVariables {
		if !strings.Contains(string(output), name) {
			t.Errorf("startup error does not name %s", name)
		}
	}
}

func TestMainExitsWhenSetupFails(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		body        string
		unavailable bool
		hang        bool
		wantLog     string
	}{
		{
			name:    "invalid credentials",
			status:  http.StatusUnauthorized,
			body:    `{"code":"unauthenticated","msg":"invalid API credentials"}`,
			wantLog: "invalid API credentials",
		},
		{
			name:        "server unavailable",
			unavailable: true,
			wantLog:     "connection refused",
		},
		{
			name:    "server timeout",
			hang:    true,
			wantLog: "context deadline exceeded",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.hang {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				if _, err := io.WriteString(w, test.body); err != nil {
					t.Errorf("write response: %v", err)
				}
			}))
			t.Cleanup(server.Close)
			if test.unavailable {
				server.Close()
			}

			setTestEnvironment(t)
			t.Setenv("LIVEKIT_URL", strings.Replace(server.URL, "http://", "ws://", 1))
			started := time.Now()
			output, err := runBridgeMain(t)
			if exitError, ok := err.(*exec.ExitError); !ok || exitError.ExitCode() != 1 {
				t.Fatalf("expected exit code 1, got %v: %s", err, output)
			}
			if !strings.Contains(string(output), "LiveKit inbound setup failed") {
				t.Errorf("missing startup failure context: %s", output)
			}
			if strings.Contains(string(output), "setup succeeded") {
				t.Errorf("failed startup reported success: %s", output)
			}
			if !strings.Contains(string(output), test.wantLog) {
				t.Errorf("expected log containing %q, got: %s", test.wantLog, output)
			}
			if test.hang && time.Since(started) < 5*time.Second {
				t.Error("startup timed out before the five-second deadline")
			}
		})
	}
}

func TestMainServesWebhooksUntilStopped(t *testing.T) {
	// Reply with empty protobuf, which suits every list and create request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/protobuf")
	}))
	t.Cleanup(server.Close)
	setTestEnvironment(t)
	t.Setenv("LIVEKIT_URL", strings.Replace(server.URL, "http://", "ws://", 1))

	// Start the bridge and wait until it listens
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := bridgeMainCommand(ctx, t)
	logs, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(logs)
	var output strings.Builder
	var address string
	for address == "" && lines.Scan() {
		output.WriteString(lines.Text() + "\n")
		_, address, _ = strings.Cut(lines.Text(), "Listening for LiveKit webhooks on ")
	}
	if address == "" {
		t.Fatalf("the bridge did not start listening: %s", output.String())
	}

	// Check that the webhook rejects unsigned requests
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post("http://127.0.0.1:"+port+"/livekit/webhook", "application/webhook+json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401 for an unsigned webhook, got %d", response.StatusCode)
	}

	// Stop the bridge the way Docker does
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	for lines.Scan() {
		output.WriteString(lines.Text() + "\n")
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("expected a clean exit, got %v: %s", err, output.String())
	}
	for _, expected := range []string{"LiveKit inbound setup succeeded", "LiveKit outbound setup succeeded", "Bridge stopped"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("expected log containing %q, got: %s", expected, output.String())
		}
	}
}

func TestBridgeMainProcess(t *testing.T) {
	if os.Getenv("BRIDGE_TEST_MAIN") == "1" {
		main()
	}
}

// bridgeMainCommand prepares a process that runs main, and stops it when ctx ends.
func bridgeMainCommand(ctx context.Context, t *testing.T) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, executable, "-test.run=^TestBridgeMainProcess$")
	command.Dir = filepath.Join(t.TempDir(), "apps", "bridge")
	if err := os.MkdirAll(command.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	command.Env = append(os.Environ(), "BRIDGE_TEST_MAIN=1")
	return command
}

// runBridgeMain runs main until it exits, and returns everything it logged.
func runBridgeMain(t *testing.T) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	output, err := bridgeMainCommand(ctx, t).CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("startup did not finish within ten seconds: %v: %s", ctx.Err(), output)
	}
	return output, err
}
