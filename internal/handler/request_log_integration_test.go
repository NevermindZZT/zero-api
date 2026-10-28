package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/adapter"
	"github.com/never/zero-api/internal/diagnostics"
	"github.com/never/zero-api/internal/store"
)

func TestRequestFailureLogPersistsHTTP200TruncationWithoutSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Session_id") != "SECRET_SESSION" {
			t.Error("native session header was not preserved")
		}
		if !strings.HasPrefix(r.Header.Get("X-Request-Id"), "req_") {
			t.Error("local correlation id not forwarded")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-Id", "provider_correlation_id")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"SECRET_RESPONSE_CONTENT\"}\n\n")
	}))
	defer upstream.Close()
	logPath := filepath.Join(t.TempDir(), "logs", "request-errors.jsonl")
	logger, err := diagnostics.New(diagnostics.Options{Path: logPath})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	h := &ProxyHandler{breaker: NewCircuitBreaker()}
	h.SetDiagnosticRecorder(logger)
	r := gin.New()
	r.Use(diagnostics.RequestMiddleware(logger))
	r.POST("/v1/responses", func(c *gin.Context) {
		raw, _ := io.ReadAll(c.Request.Body)
		ch := &store.Channel{ID: 7, Type: "responses", BaseURL: upstream.URL, APIKey: "sk-SECRET_UPSTREAM_KEY"}
		err := h.tryForward(c, raw, &store.Model{ModelID: "mock-model"}, ch, nil, time.Second, &store.ProxyConfigData{}, true, adapter.NewDownstreamAdapter("responses"))
		if err == nil {
			t.Error("truncation was treated as success")
			return
		}
		h.finishForwardFailure(c, ch, err)
	})
	request := httptest.NewRequest("POST", "/v1/responses?token=SECRET_QUERY", strings.NewReader(`{"model":"mock-model","stream":true,"input":"SECRET_PROMPT","previous_response_id":"SECRET_PREVIOUS_RESPONSE"}`))
	request.Header.Set("Authorization", "Bearer SECRET_AUTH")
	request.Header.Set("Session_id", "SECRET_SESSION")
	request.Header.Set("X-Request-Id", "SECRET_INCOMING_REQUEST_ID")
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("status=%d", response.Code)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SECRET") {
		t.Fatalf("sensitive data persisted: %s", data)
	}
	var event diagnostics.Event
	if err = json.Unmarshal([]byte(strings.TrimSpace(string(data))), &event); err != nil {
		t.Fatalf("expected one anomaly event: %v: %s", err, data)
	}
	if event.RequestID == "" || event.RequestID != response.Header().Get("X-Request-Id") {
		t.Fatalf("correlation mismatch: %+v", event)
	}
	if event.UpstreamRequestID != "provider_correlation_id" || event.Category != "stream_truncated" || event.Stage != "attempt" || event.Status != 200 || event.UpstreamStatus != 200 || !event.ResponseStarted || event.BytesWritten <= 0 || event.Retryable {
		t.Fatalf("incomplete diagnostics: %+v", event)
	}
}
