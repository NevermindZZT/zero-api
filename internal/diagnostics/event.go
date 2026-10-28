// Package diagnostics writes bounded, redacted request failure metadata only.
package diagnostics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

type Recorder interface{ Record(Event) }

type Event struct {
	Timestamp         time.Time `json:"timestamp"`
	RequestID         string    `json:"request_id"`
	UpstreamRequestID string    `json:"upstream_request_id,omitempty"`
	UpstreamErrorCode string    `json:"upstream_error_code,omitempty"`
	Method            string    `json:"method"`
	Path              string    `json:"path"`
	Protocol          string    `json:"protocol"`
	Model             string    `json:"model,omitempty"`
	ChannelID         int64     `json:"channel_id,omitempty"`
	ChannelType       string    `json:"channel_type,omitempty"`
	Attempt           int       `json:"attempt,omitempty"`
	Stream            bool      `json:"stream"`
	Stage             string    `json:"stage"`
	Category          string    `json:"category"`
	Status            int       `json:"status"`
	UpstreamStatus    int       `json:"upstream_status,omitempty"`
	DurationMS        int64     `json:"duration_ms"`
	ResponseStarted   bool      `json:"response_started"`
	BytesWritten      int64     `json:"bytes_written"`
	Retryable         bool      `json:"retryable"`
}

type requestIDKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, safeID(id))
}
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

var fallbackCounter atomic.Uint64

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "req_" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("req_%016x%016x", uint64(time.Now().UnixNano()), fallbackCounter.Add(1))
}

// RequestMiddleware deliberately never reads the body, query, or incoming request ID.
// Attempt events carry additional metadata supplied by the forwarding handler.
func RequestMiddleware(rec Recorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path != "/v1" && !strings.HasPrefix(c.Request.URL.Path, "/v1/") {
			c.Next()
			return
		}
		id := newRequestID()
		c.Request = c.Request.WithContext(WithRequestID(c.Request.Context(), id))
		c.Header("X-Request-Id", id)
		start := time.Now()
		defer func() {
			panicked := recover()
			status := c.Writer.Status()
			if panicked != nil {
				status = http.StatusInternalServerError
			}
			cancelled := c.Request.Context().Err() != nil
			if rec != nil && (panicked != nil || cancelled || status >= 400) {
				category := "upstream_status"
				switch {
				case panicked != nil:
					category = "internal"
				case cancelled:
					category = "client_cancelled"
				case status == 400 || status == 422:
					category = "invalid_request"
				case status == 401:
					category = "authentication"
				case status == 403:
					category = "permission"
				case status == 404:
					category = "model_unavailable"
				case status >= 500:
					category = "internal"
				}
				path := StaticPath(c.Request.URL.Path)
				rec.Record(Event{RequestID: id, Method: c.Request.Method, Path: path, Protocol: protocolForPath(path), Stage: "request", Category: category, Status: status, DurationMS: time.Since(start).Milliseconds(), ResponseStarted: c.Writer.Written(), BytesWritten: int64(c.Writer.Size())})
			}
			// Preserve recovery ownership; the panic value is never serialized.
			if panicked != nil {
				panic(panicked)
			}
		}()
		c.Next()
	}
}

// StaticPath only allows known fixed API routes; dynamic IDs and arbitrary paths are discarded.
func StaticPath(path string) string {
	switch path {
	case "/v1/chat/completions", "/v1/messages", "/v1/responses", "/v1/models", "/v1/embeddings", "/v1/images/generations", "/v1/images/edits", "/v1/images/variations", "/v1/audio/speech", "/v1/audio/transcriptions", "/v1/audio/translations", "/v1/moderations", "/v1/batches":
		return path
	default:
		return "/v1/unknown"
	}
}
func protocolForPath(path string) string {
	switch path {
	case "/v1/messages":
		return "anthropic"
	case "/v1/responses":
		return "responses"
	default:
		return "openai"
	}
}
func allowed(value string, choices ...string) string {
	for _, choice := range choices {
		if value == choice {
			return value
		}
	}
	return "unknown"
}
func safeID(value string) string {
	if len(value) == 0 || len(value) > 96 || sensitive(value) {
		return ""
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return ""
		}
	}
	return value
}
func sensitive(value string) bool {
	v := strings.ToLower(value)
	return strings.HasPrefix(v, "sk-") || strings.HasPrefix(v, "sk_") || strings.HasPrefix(v, "bearer") || strings.HasPrefix(v, "token") || strings.HasPrefix(v, "session") || strings.HasPrefix(v, "http") || strings.Contains(v, "api_key") || strings.Contains(v, "api-key")
}
func safeModel(value string) string {
	if len(value) == 0 || len(value) > 80 || sensitive(value) {
		return ""
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:/-", r)) {
			return ""
		}
	}
	return value
}
func bounded(v, min, max int64) int64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
func normalize(e Event) Event {
	e.Timestamp = time.Now().UTC()
	e.RequestID = safeID(e.RequestID)
	e.UpstreamRequestID = safeID(e.UpstreamRequestID)
	if e.UpstreamErrorCode != "" {
		code := allowed(e.UpstreamErrorCode, "context_length_exceeded", "rate_limit_exceeded", "insufficient_quota", "invalid_api_key", "authentication_error", "permission_denied", "previous_response_not_found", "tool_call_mismatch", "invalid_request", "server_error", "unknown")
		if code == "unknown" && e.UpstreamErrorCode != "unknown" {
			code = ""
		}
		e.UpstreamErrorCode = code
	}
	e.Method = allowed(e.Method, http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions)
	e.Path = StaticPath(e.Path)
	e.Protocol = allowed(e.Protocol, "openai", "responses", "anthropic", "gemini")
	e.Model = safeModel(e.Model)
	e.ChannelType = allowed(e.ChannelType, "openai", "openrouter", "responses", "anthropic", "gemini")
	if e.ChannelType == "unknown" {
		e.ChannelType = ""
	}
	e.Stage = allowed(e.Stage, "request", "attempt")
	e.Category = allowed(e.Category, "invalid_request", "authentication", "permission", "model_unavailable", "upstream_status", "upstream_transport", "upstream_timeout", "upstream_stream", "protocol_conversion", "client_cancelled", "internal", "unknown", "circuit_open", "upstream_incomplete", "upstream_error", "downstream_write", "request_conversion", "response_conversion", "stream_truncated", "stream_idle_timeout", "upstream_http_error", "upstream_transport_error", "conversion_error", "probe_failed", "stream_failed", "stream_incomplete")
	e.ChannelID = bounded(e.ChannelID, 0, 1<<53)
	e.Attempt = int(bounded(int64(e.Attempt), 0, 1000))
	if e.Status < 100 || e.Status > 599 {
		e.Status = 0
	}
	if e.UpstreamStatus < 100 || e.UpstreamStatus > 599 {
		e.UpstreamStatus = 0
	}
	e.DurationMS = bounded(e.DurationMS, 0, 24*60*60*1000)
	e.BytesWritten = bounded(e.BytesWritten, 0, 1<<50)
	return e
}
