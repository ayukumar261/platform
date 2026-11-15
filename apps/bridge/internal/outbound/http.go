package outbound

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/google/uuid"
)

var phonePattern = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

type Request struct {
	CallID string
	To     string
}

type Handler struct {
	running context.Context
	token   [32]byte
	enabled bool
	dial    func(context.Context, Request) error
	mu      sync.Mutex
	closing bool
	calls   sync.WaitGroup
}

// NewHandler prepares the private POST /call endpoint without placing a call.
//
// Parameters:
//   - running: ends active calls when the bridge shuts down
//   - token: the shared BRIDGE_API_TOKEN supplied by the Express API
//   - dial: runs one call until it finishes, and observes running's cancellation
//
// Returns:
//   - a handler whose accepted calls outlive the HTTP requests that started them
func NewHandler(running context.Context, token string, dial func(context.Context, Request) error) *Handler {
	return &Handler{running: running, token: sha256.Sum256([]byte(token)), enabled: token != "", dial: dial}
}

// ServeHTTP validates a request, starts its call, and returns HTTP 202.
//
// Acceptance means that work was scheduled in this process, not that the phone
// answered. A process restart loses this work. Each POST starts a new call;
// callers must not automatically retry a request after an ambiguous timeout.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Accept only call creation requests
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST to request a call.")
		return
	}

	// Authenticate before reading the body
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "unauthorized", "Provide a valid bearer token.")
		return
	}
	supplied := sha256.Sum256([]byte(parts[1]))
	if !h.enabled || subtle.ConstantTimeCompare(supplied[:], h.token[:]) != 1 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "unauthorized", "Provide a valid bearer token.")
		return
	}

	// Require an uncompressed JSON body
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	encoding := r.Header.Get("Content-Encoding")
	charset := parameters["charset"]
	if err != nil || mediaType != "application/json" ||
		(charset != "" && !strings.EqualFold(charset, "utf-8")) ||
		(encoding != "" && !strings.EqualFold(encoding, "identity")) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Send uncompressed JSON encoded as UTF-8.")
		return
	}

	// Decode one small request without accepting unknown fields
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var body struct {
		To string `json:"to"`
	}
	err = decoder.Decode(&body)
	if err == nil {
		var extra any
		if trailing := decoder.Decode(&extra); trailing != io.EOF {
			if trailing == nil {
				trailing = errors.New("multiple JSON values")
			}
			err = trailing
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "Keep the request body within 4 KB.")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", "Provide one JSON object containing only to.")
		}
		return
	}

	// Check the destination before starting any work
	if !phonePattern.MatchString(body.To) {
		writeError(w, http.StatusBadRequest, "invalid_phone_number", "Provide to in international format, such as +15551234567.")
		return
	}

	// Register the call before replying or allowing shutdown to wait
	request := Request{CallID: uuid.NewString(), To: body.To}
	h.mu.Lock()
	if h.closing || h.running.Err() != nil {
		h.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "bridge_unavailable", "The bridge is stopping.")
		return
	}
	h.calls.Go(func() {
		log.Printf("Starting outbound call %s", request.CallID)
		if err := h.dial(h.running, request); err != nil {
			log.Printf("Outbound call %s failed: %v", request.CallID, err)
			return
		}
		log.Printf("Outbound call %s ended", request.CallID)
	})
	h.mu.Unlock()

	// Return the ID used in the room name and bridge logs
	writeJSON(w, http.StatusAccepted, map[string]string{"callId": request.CallID})
}

// Shutdown stops accepting calls and waits for accepted calls to finish.
// Cancel the context passed to NewHandler first to interrupt active calls.
func (h *Handler) Shutdown() {
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	h.calls.Wait()
}

// writeError sends the same error envelope used by the Express API.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// writeJSON sets the status and content type before encoding the response.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
