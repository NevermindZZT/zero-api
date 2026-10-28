package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/diagnostics"
	"github.com/never/zero-api/internal/store"
)

// forwardError separates response commitment, retry policy, and channel health.
// Error deliberately contains only a controlled category, never upstream bodies,
// URLs, credentials, or provider error messages.
type forwardError struct {
	category          string
	status            int
	upstreamStatus    int
	upstreamErrorCode string
	streamReported    bool
	started           bool
	retryable         bool
	clientCancelled   bool
	breakerFailure    bool
	cause             error
}

func (e *forwardError) Error() string { return e.category }
func (e *forwardError) Unwrap() error { return e.cause }
func forwardingFailure(category string, cause error, retryable, breakerFailure bool) *forwardError {
	return &forwardError{category: category, status: http.StatusBadGateway, retryable: retryable, breakerFailure: breakerFailure, cause: cause}
}
func transportFailure(ctx context.Context, err error) *forwardError {
	if ctx.Err() != nil {
		return &forwardError{category: "client_cancelled", clientCancelled: true, cause: err}
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return forwardingFailure("upstream_timeout", err, true, true)
	}
	return forwardingFailure("upstream_transport_error", err, true, true)
}
func forwardOutcome(err error, started bool) *forwardError {
	var outcome *forwardError
	if !errors.As(err, &outcome) {
		outcome = forwardingFailure("internal", err, false, false)
	}
	// A copy lets callers annotate commitment without mutating an error shared by
	// a watcher or another layer.
	copy := *outcome
	copy.started = copy.started || started
	if copy.started || copy.clientCancelled {
		copy.retryable = false
	}
	return &copy
}
func (h *ProxyHandler) SetDiagnosticRecorder(recorder diagnostics.Recorder) { h.recorder = recorder }
func (h *ProxyHandler) recordForwardAttempt(c *gin.Context, model *store.Model, ch *store.Channel, protocol string, stream bool, started time.Time, initialBytes, upstreamStatus int, upstreamID string, err error) {
	if h.recorder == nil || err == nil {
		return
	}
	outcome := forwardOutcome(err, c.Writer.Written())
	attempt := c.GetInt("forward_attempt")
	if attempt < 1 {
		attempt = 1
	}
	written := c.Writer.Size() - initialBytes
	if initialBytes < 0 {
		written = c.Writer.Size()
	}
	status := outcome.status
	if outcome.started {
		status = c.Writer.Status()
	}
	if written < 0 {
		written = 0
	}
	h.recorder.Record(diagnostics.Event{
		RequestID: diagnostics.RequestID(c.Request.Context()), UpstreamRequestID: upstreamID,
		Method: c.Request.Method, Path: c.Request.URL.Path, Protocol: protocol, Model: model.ModelID,
		ChannelID: ch.ID, ChannelType: ch.Type, Attempt: attempt, Stream: stream, Stage: "attempt",
		Category: outcome.category, Status: status, UpstreamStatus: upstreamStatus, UpstreamErrorCode: outcome.upstreamErrorCode,
		DurationMS: time.Since(started).Milliseconds(), ResponseStarted: outcome.started,
		BytesWritten: int64(written), Retryable: outcome.retryable,
	})
}

// finishForwardFailure never appends a second HTTP response to an SSE stream.
// Client cancellation and model/input errors do not poison channel health.
func (h *ProxyHandler) finishForwardFailure(c *gin.Context, ch *store.Channel, err error) (retry bool) {
	outcome := forwardOutcome(err, c.Writer.Written())
	if outcome.breakerFailure && !outcome.clientCancelled && h.breaker != nil {
		h.breaker.RecordFailure(ch.ID)
	}
	if outcome.clientCancelled || outcome.started {
		return false
	}
	if outcome.retryable {
		return true
	}
	status := outcome.status
	if status < 400 {
		status = http.StatusBadGateway
	}
	c.JSON(status, gin.H{"error": gin.H{"type": outcome.category, "message": "Request forwarding failed"}})
	return false
}
func forwardRequestID(c *gin.Context, req *http.Request) {
	// Only the locally generated context ID is forwarded, never the incoming header.
	if id := diagnostics.RequestID(c.Request.Context()); id != "" {
		req.Header.Set("X-Request-Id", id)
	}
}
func copyForwardHeaders(c *gin.Context, headers http.Header, transformed bool) {
	for name, values := range filterHopByHop(headers) {
		if strings.EqualFold(name, "X-Request-Id") && diagnostics.RequestID(c.Request.Context()) != "" {
			continue
		}
		if transformed && strings.EqualFold(name, "Content-Length") {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(name, value)
		}
	}
	if transformed {
		c.Writer.Header().Del("Content-Length")
	}
}

func writeForwardBody(c *gin.Context, status int, contentType string, data []byte, timeout time.Duration) error {
	if c.Request.Context().Err() != nil {
		return transportFailure(c.Request.Context(), c.Request.Context().Err())
	}
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetWriteDeadline(time.Now().Add(forwardTimeout(timeout)))
	if contentType != "" {
		c.Header("Content-Type", contentType)
	}
	c.Status(status)
	if _, err := c.Writer.Write(data); err != nil {
		if c.Request.Context().Err() != nil {
			return &forwardError{category: "client_cancelled", clientCancelled: true, cause: err}
		}
		return forwardingFailure("downstream_write", err, false, false)
	}
	return nil
}

// providerErrorCode returns only a fixed, safe enum. Provider text is inspected
// in memory for known signatures and is never returned or retained.
func providerErrorCode(data []byte, status int) string {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Response struct {
			Error struct {
				Code    string `json:"code"`
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"response"`
		Code    string `json:"code"`
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &payload)
	code, kind, message := payload.Error.Code, payload.Error.Type, payload.Error.Message
	if code == "" && kind == "" {
		code, kind, message = payload.Response.Error.Code, payload.Response.Error.Type, payload.Response.Error.Message
	}
	if code == "" && kind == "" {
		code, kind, message = payload.Code, payload.Type, payload.Message
	}
	for _, value := range []string{code, kind} {
		switch value {
		case "context_length_exceeded", "rate_limit_exceeded", "insufficient_quota", "invalid_api_key", "authentication_error", "permission_denied", "previous_response_not_found", "tool_call_mismatch", "invalid_request", "server_error":
			return value
		case "invalid_request_error":
			// Generic type; inspect known message signatures below first.
		}
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "no tool call found") {
		return "tool_call_mismatch"
	}
	if strings.Contains(lower, "previous response") && strings.Contains(lower, "not found") {
		return "previous_response_not_found"
	}
	if code == "invalid_request_error" || kind == "invalid_request_error" {
		return "invalid_request"
	}
	switch status {
	case 401:
		return "authentication_error"
	case 403:
		return "permission_denied"
	case 429:
		return "rate_limit_exceeded"
	}
	if status >= 500 {
		return "server_error"
	}
	return "unknown"
}

func upstreamStatusFailure(c *gin.Context, resp *http.Response, timeout time.Duration) error {
	status := resp.StatusCode
	retryable := status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
	outcome := &forwardError{category: "upstream_http_error", status: status, upstreamStatus: status, retryable: retryable, breakerFailure: retryable}
	// Read only a bounded error envelope, with a body deadline even for a stream
	// client whose total timeout is intentionally disabled.
	timer := time.AfterFunc(forwardTimeout(timeout), func() { _ = resp.Body.Close() })
	defer timer.Stop()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if c.Request.Context().Err() != nil {
		return transportFailure(c.Request.Context(), c.Request.Context().Err())
	}
	if err != nil {
		return transportFailure(c.Request.Context(), err)
	}
	outcome.upstreamErrorCode = providerErrorCode(data, status)
	if retryable || c.Writer.Written() {
		outcome.started = c.Writer.Written()
		return outcome
	}
	if len(data) > 1<<20 {
		return forwardingFailure("upstream_transport_error", nil, false, false)
	}
	copyForwardHeaders(c, resp.Header, true)
	if err := writeForwardBody(c, status, resp.Header.Get("Content-Type"), data, timeout); err != nil {
		return err
	}
	outcome.started = c.Writer.Written()
	return outcome
}

// HTTP 2xx is not sufficient for Responses (or compatible providers): a
// provider may return a failed/incomplete object, or a top-level error envelope.
func nonstreamSemanticOutcome(data []byte) *forwardError {
	var value struct {
		Status  string          `json:"status"`
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &value) != nil {
		return nil
	}
	failed := value.Status == "failed" || value.Status == "cancelled" || len(value.Error) > 0 && string(value.Error) != "null"
	incomplete := value.Status == "incomplete"
	for _, choice := range value.Choices {
		if choice.FinishReason == "length" || choice.FinishReason == "content_filter" {
			incomplete = true
		}
	}
	if failed {
		return &forwardError{category: "upstream_error", status: http.StatusBadGateway, breakerFailure: true, upstreamErrorCode: providerErrorCode(data, 200)}
	}
	if incomplete {
		return &forwardError{category: "upstream_incomplete", status: http.StatusOK}
	}
	return nil
}

func writeCommittedStreamError(c *gin.Context, protocol, category string, timeout time.Duration) {
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetWriteDeadline(time.Now().Add(forwardTimeout(timeout)))
	_, _ = c.Writer.Write(streamErrorPayload(protocol, category))
	_ = controller.Flush()
}

func forwardTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 60 * time.Second
	}
	return timeout
}
