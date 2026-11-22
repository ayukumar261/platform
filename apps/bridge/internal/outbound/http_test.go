package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Constants
const callBody = `{"to":"+15551234567","provider":"cartesia","providerAgentId":"agent_123"}`

// callRequest prepares a private request without contacting a carrier.
//
// Parameters:
//   - body: the JSON request body
//
// Returns:
//   - a POST /call request with the test token and a JSON content type
func callRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/call", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-bridge-token")
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestHandlerAcceptsCallAndKeepsItAfterRequestEnds(t *testing.T) {
	running, stop := context.WithCancel(t.Context())
	started := make(chan Request, 1)
	dialContext := make(chan context.Context, 1)
	ended := make(chan struct{})
	handler := NewHandler(running, "test-bridge-token", func(ctx context.Context, request Request) error {
		started <- request
		dialContext <- ctx
		<-ctx.Done()
		close(ended)
		return ctx.Err()
	})
	t.Cleanup(func() { stop(); handler.Shutdown() })

	// Accept the request before the conversation finishes
	requestContext, cancelRequest := context.WithCancel(t.Context())
	request := callRequest(callBody).WithContext(requestContext)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", response.Code, response.Body)
	}
	var accepted struct {
		CallID string `json:"callId"`
	}
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&accepted); err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(accepted.CallID); err != nil {
		t.Fatalf("unexpected acceptance: %+v", accepted)
	}
	select {
	case call := <-started:
		expected := Request{CallID: accepted.CallID, To: "+15551234567", Provider: "cartesia", ProviderAgentID: "agent_123"}
		if call != expected {
			t.Fatalf("unexpected dial request: %+v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("accepted call did not start")
	}

	// End the HTTP request without ending the call
	cancelRequest()
	ctx := <-dialContext
	if ctx.Err() != nil {
		t.Fatalf("HTTP cancellation reached the call: %v", ctx.Err())
	}
	select {
	case <-ended:
		t.Fatal("call ended with the HTTP request")
	default:
	}

	// Stop the bridge and wait for its call to end
	stop()
	handler.Shutdown()
	select {
	case <-ended:
	default:
		t.Fatal("shutdown returned before the call ended")
	}
}

func TestHandlerRejectsRequestsBeforeDialing(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		header string
		value  string
		method string
		status int
	}{
		{name: "missing token", body: `{}`, header: "Authorization", status: 401},
		{name: "wrong token", body: `{`, header: "Authorization", value: "Bearer wrong", status: 401},
		{name: "wrong scheme", body: `{}`, header: "Authorization", value: "Basic test-bridge-token", status: 401},
		{name: "wrong method", method: "GET", status: 405},
		{name: "wrong content type", body: `{}`, header: "Content-Type", value: "text/plain", status: 415},
		{name: "unsupported encoding", body: `{}`, header: "Content-Encoding", value: "gzip", status: 415},
		{name: "unsupported charset", body: `{}`, header: "Content-Type", value: "application/json; charset=iso-8859-1", status: 415},
		{name: "empty body", status: 400},
		{name: "malformed JSON", body: `{`, status: 400},
		{name: "null", body: `null`, status: 400},
		{name: "array", body: `[]`, status: 400},
		{name: "missing number", body: `{}`, status: 400},
		{name: "numeric number", body: `{"to":15551234567}`, status: 400},
		{name: "local number", body: `{"to":"5551234567"}`, status: 400},
		{name: "zero country code", body: `{"to":"+0123456789"}`, status: 400},
		{name: "long number", body: `{"to":"+1234567890123456"}`, status: 400},
		{name: "number suffix", body: `{"to":"+15551234567\n"}`, status: 400},
		{name: "SIP address", body: `{"to":"sip:someone@example.com"}`, status: 400},
		{name: "missing provider", body: `{"to":"+15551234567","providerAgentId":"agent_123"}`, status: 400},
		{name: "blank provider", body: `{"to":"+15551234567","provider":" ","providerAgentId":"agent_123"}`, status: 400},
		{name: "missing provider agent ID", body: `{"to":"+15551234567","provider":"cartesia"}`, status: 400},
		{name: "blank provider agent ID", body: `{"to":"+15551234567","provider":"cartesia","providerAgentId":" "}`, status: 400},
		{name: "unknown field", body: `{"to":"+15551234567","carrier":"telnyx"}`, status: 400},
		{name: "trailing JSON", body: `{"to":"+15551234567"} {}`, status: 400},
		{name: "trailing garbage", body: `{"to":"+15551234567"} garbage`, status: 400},
		{name: "large body", body: `{"to":"` + strings.Repeat("1", 5000) + `"}`, status: 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			var called bool
			handler := NewHandler(t.Context(), "test-bridge-token", func(context.Context, Request) error {
				called = true
				return nil
			})
			request := callRequest(test.body)
			if test.header != "" {
				request.Header.Set(test.header, test.value)
			}
			if test.method != "" {
				request.Method = test.method
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			handler.Shutdown()
			if called {
				t.Fatal("invalid request started a call")
			}
			if response.Code != test.status {
				t.Fatalf("expected %d, got %d: %s", test.status, response.Code, response.Body)
			}
			var body struct {
				Error struct{ Code, Message string }
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error.Code == "" || body.Error.Message == "" {
				t.Fatalf("expected an error envelope, got %s", response.Body)
			}
		})
	}
}

func TestHandlerRejectsWorkAfterShutdownOrCancellation(t *testing.T) {
	for _, state := range []string{"shutdown", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var called bool
			handler := NewHandler(ctx, "test-bridge-token", func(context.Context, Request) error {
				called = true
				return nil
			})
			if state == "shutdown" {
				handler.Shutdown()
			} else {
				cancel()
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, callRequest(callBody))
			handler.Shutdown()
			if response.Code != http.StatusServiceUnavailable || called {
				t.Fatalf("expected 503 without dialing, got %d, called=%v", response.Code, called)
			}
		})
	}
}

func TestHandlerReleasesFailedCallsAndAssignsDistinctIDs(t *testing.T) {
	var mu sync.Mutex
	var ids []string
	handler := NewHandler(t.Context(), "test-bridge-token", func(_ context.Context, request Request) error {
		mu.Lock()
		ids = append(ids, request.CallID)
		mu.Unlock()
		return errors.New("dial failed")
	})
	for range 2 {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, callRequest(callBody))
		if response.Code != http.StatusAccepted {
			t.Fatalf("unexpected status %d", response.Code)
		}
	}
	handler.Shutdown()
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("expected two distinct call IDs, got %v", ids)
	}
}

func TestHandlerWithoutTokenFailsClosed(t *testing.T) {
	handler := NewHandler(t.Context(), "", func(context.Context, Request) error {
		t.Error("handler without a configured token started a call")
		return nil
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, callRequest(callBody))
	handler.Shutdown()
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status %d", response.Code)
	}
}
