package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/adapter"
	"github.com/never/zero-api/internal/diagnostics"
	"github.com/never/zero-api/internal/metrics"
	"github.com/never/zero-api/internal/store"
)

type forwardingRecorder struct{ events []diagnostics.Event }

func (r *forwardingRecorder) Record(e diagnostics.Event) { r.events = append(r.events, e) }

type forwardingBrokenBody struct{}

func (forwardingBrokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (forwardingBrokenBody) Close() error             { return nil }
func forwardingContext(path string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	c.Request = c.Request.WithContext(diagnostics.WithRequestID(c.Request.Context(), "req_local_audit"))
	c.Header("X-Request-Id", "req_local_audit")
	return c, recorder
}
func forwardingTestCall(h *ProxyHandler, c *gin.Context, base, kind, protocol string, timeout time.Duration) error {
	return h.tryForward(c, []byte(`{"model":"test-model","stream":true,"messages":[],"input":"test"}`), &store.Model{ModelID: "test-model"}, &store.Channel{ID: 7, Type: kind, BaseURL: base, Name: "mock"}, nil, timeout, &store.ProxyConfigData{}, true, adapter.NewDownstreamAdapter(protocol))
}
func forwardingRequireError(t *testing.T, err error, category string) *forwardError {
	t.Helper()
	var outcome *forwardError
	if !errors.As(err, &outcome) || outcome.category != category {
		t.Fatalf("outcome=%v, want %s", err, category)
	}
	return outcome
}

func TestForwardStreamUnexpectedEOFIsUpstreamFailure(t *testing.T) {
	c, w := forwardingContext("/v1/chat/completions")
	resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: forwardingBrokenBody{}}
	h := &ProxyHandler{}
	err := h.streamResponse(c, resp, &adapter.OpenAIAdapter{}, &store.Model{ModelID: "test-model"}, &store.Channel{Type: "openai"}, nil, time.Now(), time.Second, adapter.NewPassthroughDownstreamAdapter("openai"))
	outcome := forwardingRequireError(t, err, "stream_truncated")
	if outcome.clientCancelled || outcome.started || !outcome.retryable || !outcome.breakerFailure || w.Body.Len() != 0 {
		t.Fatalf("wrong outcome: %+v body=%q", outcome, w.Body.String())
	}
}
func TestForwardStreamIdleAfterOutputIsNotSuccessOrRetry(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	c, w := forwardingContext("/v1/chat/completions")
	h := &ProxyHandler{breaker: NewCircuitBreaker()}
	outcome := forwardingRequireError(t, forwardingTestCall(h, c, upstream.URL, "openai", "openai", 50*time.Millisecond), "stream_idle_timeout")
	if !outcome.started || outcome.retryable || !outcome.breakerFailure {
		t.Fatalf("wrong outcome: %+v", outcome)
	}
	if !strings.Contains(w.Body.String(), "stream_idle_timeout") || strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("wrong SSE ending: %s", w.Body.String())
	}
	if h.finishForwardFailure(c, &store.Channel{ID: 7}, outcome) {
		t.Fatal("committed stream must not fail over")
	}
	if allow, _ := h.breaker.MayProceed(7); allow {
		t.Fatal("failed stream must not mark channel healthy")
	}
}
func TestForwardStreamClientCancelReachesUpstream(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": ping\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer upstream.Close()
	c, _ := forwardingContext("/v1/chat/completions")
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	h := &ProxyHandler{breaker: NewCircuitBreaker()}
	done := make(chan error, 1)
	go func() { done <- forwardingTestCall(h, c, upstream.URL, "openai", "openai", 5*time.Second) }()
	<-started
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not reach upstream")
	}
	var err error
	select {
	case err = <-done:
	case <-time.After(time.Second):
		t.Fatal("handler remained blocked after cancel")
	}
	outcome := forwardingRequireError(t, err, "client_cancelled")
	if !outcome.clientCancelled || outcome.retryable || outcome.breakerFailure {
		t.Fatalf("wrong outcome %+v", outcome)
	}
	if h.finishForwardFailure(c, &store.Channel{ID: 7}, err) {
		t.Fatal("cancelled request must not fail over")
	}
	if allow, _ := h.breaker.MayProceed(7); !allow {
		t.Fatal("client cancel must not poison channel")
	}
}
func TestForwardStreamHTTP400KeepsJSONAndSafeErrorCode(t *testing.T) {
	body := `{"error":{"type":"invalid_request_error","message":"No tool call found for function call output with call_id call_private_test"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Request-Id") != "req_local_audit" {
			t.Error("locally generated request ID was not sent")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "provider_request")
		w.WriteHeader(400)
		_, _ = io.WriteString(w, body)
	}))
	defer upstream.Close()
	c, w := forwardingContext("/v1/chat/completions")
	rec := &forwardingRecorder{}
	h := &ProxyHandler{}
	h.SetDiagnosticRecorder(rec)
	outcome := forwardingRequireError(t, forwardingTestCall(h, c, upstream.URL, "openai", "openai", time.Second), "upstream_http_error")
	if w.Code != 400 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || w.Body.String() != body {
		t.Fatalf("status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
	}
	if outcome.retryable || outcome.breakerFailure || !outcome.started {
		t.Fatalf("wrong outcome %+v", outcome)
	}
	if w.Header().Get("X-Request-Id") != "req_local_audit" {
		t.Fatal("provider request ID replaced local downstream ID")
	}
	if len(rec.events) != 1 || rec.events[0].UpstreamErrorCode != "tool_call_mismatch" || rec.events[0].UpstreamRequestID != "provider_request" {
		t.Fatalf("events=%+v", rec.events)
	}
}
func TestForwardStreamTerminalDoesNotWaitForEOF(t *testing.T) {
	terminal := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-Id", "provider_request")
		_, _ = io.WriteString(w, terminal)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer upstream.Close()
	c, w := forwardingContext("/v1/responses")
	done := make(chan error, 1)
	go func() {
		done <- forwardingTestCall(&ProxyHandler{}, c, upstream.URL, "responses", "responses", 5*time.Second)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal frame did not stop stream")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("completed stream did not release upstream")
	}
	if w.Body.String() != terminal {
		t.Fatalf("native frame changed: %q", w.Body.String())
	}
	if got := w.Header().Values("X-Request-Id"); len(got) != 1 || got[0] != "req_local_audit" {
		t.Fatalf("request IDs=%v", got)
	}
}
func TestForwardStreamFailureAndMissingTerminalCannotComplete(t *testing.T) {
	for _, tc := range []struct{ name, input, category string }{
		{"failed", "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"private-test-provider-message\"}}}\n\n", "stream_failed"},
		{"truncated", "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", "stream_truncated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w := forwardingContext("/v1/chat/completions")
			resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.input))}
			err := (&ProxyHandler{}).streamResponse(c, resp, &adapter.ResponsesAdapter{}, &store.Model{ModelID: "test-model"}, &store.Channel{Type: "responses"}, nil, time.Now(), time.Second, adapter.NewDownstreamAdapter("openai"))
			forwardingRequireError(t, err, tc.category)
			for _, forbidden := range []string{"[DONE]", `"finish_reason":"stop"`, `"finish_reason":"length"`, "private-test-provider-message"} {
				if strings.Contains(w.Body.String(), forbidden) {
					t.Fatalf("false completion or raw error leaked: %s", w.Body.String())
				}
			}
		})
	}
}
func TestForwardStreamIncompleteIsNotTransportFailure(t *testing.T) {
	frame := "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n"
	c, w := forwardingContext("/v1/responses")
	resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(frame))}
	err := (&ProxyHandler{}).streamResponse(c, resp, &adapter.ResponsesAdapter{}, &store.Model{ModelID: "test-model"}, &store.Channel{Type: "responses"}, nil, time.Now(), time.Second, adapter.NewPassthroughDownstreamAdapter("responses"))
	outcome := forwardingRequireError(t, err, "stream_incomplete")
	if outcome.breakerFailure || outcome.retryable || w.Body.String() != frame {
		t.Fatalf("wrong incomplete outcome=%+v body=%q", outcome, w.Body.String())
	}
}
func TestForwardStreamMultilineCRLFAndContentLength(t *testing.T) {
	input := "data:{\"id\":\"chatcmpl-test\",\r\ndata: \"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\r\n\r\ndata:[DONE]\r\n\r\n"
	for _, protocol := range []string{"openai", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			c, w := forwardingContext("/v1/" + protocol)
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Length": []string{"1"}}, Body: io.NopCloser(strings.NewReader(input))}
			var downstream adapter.DownstreamAdapter = adapter.NewPassthroughDownstreamAdapter("openai")
			if protocol == "responses" {
				downstream = adapter.NewDownstreamAdapter("responses")
			}
			err := (&ProxyHandler{}).streamResponse(c, resp, &adapter.OpenAIAdapter{}, &store.Model{ModelID: "test-model"}, &store.Channel{Type: "openai"}, nil, time.Now(), time.Second, downstream)
			if err != nil {
				t.Fatal(err)
			}
			if w.Header().Get("Content-Length") != "" {
				t.Fatal("stale upstream content length forwarded")
			}
			if protocol == "openai" && w.Body.String() != input {
				t.Fatalf("native framing changed: %q", w.Body.String())
			}
			if protocol == "responses" && (!strings.Contains(w.Body.String(), "response.completed") || !strings.Contains(w.Body.String(), "\"delta\":\"ok\"")) {
				t.Fatalf("multiline conversion failed: %s", w.Body.String())
			}
		})
	}
}
func TestForwardProxyConfigCacheConcurrentInvalidation(t *testing.T) {
	h := &ProxyHandler{proxyConfigCache: &store.ProxyConfigData{RequestTimeoutSeconds: 60}}
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 50; j++ {
				if h.getProxyConfig().RequestTimeoutSeconds != 60 {
					t.Error("bad config")
				}
				h.InvalidateProxyConfig()
			}
		}()
	}
	group.Wait()
}
func TestForwardStreamFrameAndCaptureAreBounded(t *testing.T) {
	var frame sseFrame
	if _, err := frame.add([]byte(strings.Repeat("x", maxSSEFrameBytes+1))); !errors.Is(err, errSSEFrameTooLarge) {
		t.Fatal("oversized frame accepted")
	}
	var capture streamCapture
	line := []byte(strings.Repeat("x", 1023) + "\n")
	for i := 0; i < maxStreamCaptureBytes/len(line)*3; i++ {
		capture.write(line)
	}
	if size := len(capture.bytes()); size > maxStreamCaptureBytes+(64<<10) {
		t.Fatalf("capture is unbounded: %d", size)
	}
}

func TestForwardStreamMultilineUsageFeedsTPS(t *testing.T) {
	first := "data:{\"type\":\"response.output_text.delta\",\"delta\":\"text\"}\n\n"
	last := "event: response.completed\ndata:{\"type\":\"response.completed\",\ndata: \"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":4,\"output_tokens\":9,\"total_tokens\":13}}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, first)
		w.(http.Flusher).Flush()
		timer := time.NewTimer(150 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, last)
		w.(http.Flusher).Flush()
	}))
	defer upstream.Close()
	monitor := metrics.NewTPSMonitor()
	h := &ProxyHandler{tpsMonitor: monitor}
	c, w := forwardingContext("/v1/responses")
	if err := forwardingTestCall(h, c, upstream.URL, "responses", "responses", time.Second); err != nil {
		t.Fatal(err)
	}
	if w.Body.String() != first+last {
		t.Fatal("native SSE bytes were changed for metrics normalization")
	}
	stats := monitor.Snapshot(time.Minute, 10)
	if len(stats) != 1 || stats[0].OutputTokens != 9 {
		t.Fatalf("normalized usage not observed: %+v", stats)
	}
	// Usage adapters receive the same normalized frame representation as TPS.
	var frame sseFrame
	for _, line := range strings.SplitAfter(last, "\n") {
		if line == "" {
			continue
		}
		dispatched, err := frame.add([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		if dispatched {
			break
		}
	}
	usage, err := (&adapter.ResponsesAdapter{}).ExtractUsage(normalizedSSEDataFrame(frame.payload()))
	if err != nil || usage.PromptTokens != 4 || usage.CompletionTokens != 9 {
		t.Fatalf("normalized usage=%+v err=%v", usage, err)
	}
}

func TestForwardStreamCommittedFailureWritesSafeDiagnosticJSONL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-Id", "provider_request")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"private_response_marker\"}}]}\n\n")
	}))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "diagnostics", "requests.jsonl")
	recorder, err := diagnostics.New(diagnostics.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recorder.Close() })
	h := &ProxyHandler{}
	h.SetDiagnosticRecorder(recorder)
	router := gin.New()
	router.Use(diagnostics.RequestMiddleware(recorder))
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		err := forwardingTestCall(h, c, upstream.URL, "openai", "openai", time.Second)
		if err == nil {
			t.Error("truncated response was marked successful")
		}
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?private_query_marker=value", strings.NewReader("private_prompt_marker"))
	request.Header.Set("Authorization", "Bearer private_auth_marker")
	request.Header.Set("session_id", "private_session_marker")
	router.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("already started response changed status: %d", response.Code)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private_response_marker", "private_prompt_marker", "private_auth_marker", "private_session_marker", "private_query_marker"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("content leaked into diagnostic JSONL")
		}
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("want one failed attempt event, got %d", len(lines))
	}
	var event diagnostics.Event
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatal(err)
	}
	if event.Category != "stream_truncated" || event.Status != 200 || !event.ResponseStarted || event.Retryable || event.RequestID == "" || event.RequestID != response.Header().Get("X-Request-Id") {
		t.Fatalf("wrong diagnostic metadata: %+v", event)
	}
}

func TestForwardStreamConverterErrorsCannotMarkSuccessfulTerminal(t *testing.T) {
	for _, tc := range []struct{ name, kind, protocol, body string }{
		{"upstream_completed_missing_call_id", "responses", "openai", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"id\":\"fc_item\",\"name\":\"tool\",\"arguments\":\"{}\"}]}}\n\n"},
		{"downstream_finish_missing_identity", "openai", "responses", "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			c, w := forwardingContext("/v1/chat/completions")
			rec := &forwardingRecorder{}
			h := &ProxyHandler{breaker: NewCircuitBreaker()}
			h.SetDiagnosticRecorder(rec)
			outcome := forwardingRequireError(t, forwardingTestCall(h, c, upstream.URL, tc.kind, tc.protocol, time.Second), "conversion_error")
			if !outcome.started || outcome.retryable || !outcome.streamReported {
				t.Fatalf("wrong converter outcome: %+v", outcome)
			}
			if strings.Contains(w.Body.String(), "response.completed") || strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatalf("converter error became successful terminal: %s", w.Body.String())
			}
			if len(rec.events) != 1 || rec.events[0].Category != "conversion_error" || rec.events[0].Status != 200 {
				t.Fatalf("missing converter failure event: %+v", rec.events)
			}
			if h.finishForwardFailure(c, &store.Channel{ID: 7}, outcome) {
				t.Fatal("converter failure must not fail over after output")
			}
		})
	}
}

func TestForwardTerminalRejectsProviderFailureStatuses(t *testing.T) {
	for _, tc := range []struct {
		protocol, event, payload string
		failed, incomplete       bool
	}{
		{"gemini", "", `{"candidates":[{"finishReason":"UNRECOGNIZED_FUTURE_REASON"}]}`, true, false},
		{"gemini", "", `{"candidates":[{"finishReason":"MAX_TOKENS"}]}`, false, true},
		{"responses", "response.completed", `{"type":"response.completed","response":{"status":"cancelled"}}`, true, false},
		{"responses", "", `{"type":"response.error"}`, true, false},
	} {
		var terminal streamTerminal
		terminal.observe(tc.protocol, tc.event, []byte(tc.payload))
		if !terminal.complete || terminal.failed != tc.failed || terminal.incomplete != tc.incomplete {
			t.Fatalf("protocol=%s event=%s wrong terminal=%+v", tc.protocol, tc.event, terminal)
		}
	}
}
func TestForwardNonstreamSemanticOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, body, category string
		started, healthFailure         bool
	}{
		{"native_failed", "responses", `{"id":"resp_failed","status":"failed","error":{"code":"server_error","message":"private_provider_message"}}`, "upstream_error", true, true},
		{"native_incomplete", "responses", `{"id":"resp_short","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}`, "upstream_incomplete", true, false},
		{"conversion_failed", "openai", `{"id":"resp_failed","status":"failed","error":{"code":"server_error","message":"private_provider_message"}}`, "upstream_error", false, true},
		{"native_success", "responses", `{"id":"resp_ok","status":"completed","output":[]}`, "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			c, w := forwardingContext("/v1/responses")
			rec := &forwardingRecorder{}
			h := &ProxyHandler{breaker: NewCircuitBreaker()}
			h.SetDiagnosticRecorder(rec)
			err := h.tryForward(c, []byte(`{"model":"test-model","stream":false,"input":"test","messages":[]}`), &store.Model{ModelID: "test-model"}, &store.Channel{ID: 7, Type: "responses", BaseURL: upstream.URL}, nil, time.Second, &store.ProxyConfigData{}, false, adapter.NewDownstreamAdapter(tc.protocol))
			if tc.category == "" {
				if err != nil || w.Body.String() != tc.body || len(rec.events) != 0 {
					t.Fatalf("ordinary success misclassified: err=%v body=%s events=%v", err, w.Body.String(), rec.events)
				}
				return
			}
			outcome := forwardingRequireError(t, err, tc.category)
			if outcome.started != tc.started || outcome.breakerFailure != tc.healthFailure || outcome.retryable {
				t.Fatalf("wrong outcome: %+v", outcome)
			}
			if tc.started && (w.Code != 200 || w.Body.String() != tc.body) {
				t.Fatalf("native failed/incomplete body changed: code=%d body=%s", w.Code, w.Body.String())
			}
			if !tc.started {
				if h.finishForwardFailure(c, &store.Channel{ID: 7}, err) {
					t.Fatal("semantic failure must not retry")
				}
				if w.Code != 502 || strings.Contains(w.Body.String(), "private_provider_message") {
					t.Fatalf("conversion failure response=%d %s", w.Code, w.Body.String())
				}
			}
			if len(rec.events) != 1 || rec.events[0].Category != tc.category {
				t.Fatalf("missing semantic failure diagnostic: %+v", rec.events)
			}
		})
	}
}

type forwardingDiscardConverter struct{}

func (forwardingDiscardConverter) Convert([]byte) []byte { return nil }
func (forwardingDiscardConverter) Finish() []byte        { return nil }

type forwardingDiscardDownstream struct{ adapter.DownstreamAdapter }

func (forwardingDiscardDownstream) NewStreamConverter() adapter.StreamConverter {
	return forwardingDiscardConverter{}
}
func TestForwardStreamConverterCannotDiscardSuccessfulTerminal(t *testing.T) {
	c, _ := forwardingContext("/v1/chat/completions")
	resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}
	downstream := forwardingDiscardDownstream{adapter.NewDownstreamAdapter("openai")}
	err := (&ProxyHandler{}).streamResponse(c, resp, &adapter.OpenAIAdapter{}, &store.Model{ModelID: "test-model"}, &store.Channel{Type: "openai"}, nil, time.Now(), time.Second, downstream)
	outcome := forwardingRequireError(t, err, "conversion_error")
	if outcome.retryable || outcome.breakerFailure {
		t.Fatalf("converter discard classified as provider failure: %+v", outcome)
	}
}
func TestForwardResponsesRequiresItsOwnTerminalEvent(t *testing.T) {
	c, _ := forwardingContext("/v1/responses")
	resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}
	err := (&ProxyHandler{}).streamResponse(c, resp, &adapter.ResponsesAdapter{}, &store.Model{ModelID: "test-model"}, &store.Channel{Type: "responses"}, nil, time.Now(), time.Second, adapter.NewPassthroughDownstreamAdapter("responses"))
	forwardingRequireError(t, err, "stream_truncated")
}

func TestForwardStreamUsageNormalizesNativeAnthropic(t *testing.T) {
	raw := []byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":5}}\n\n")
	adapt := &adapter.AnthropicAdapter{}
	data := streamUsageJSON(raw, adapt, "anthropic")
	usage, err := adapt.ExtractUsage(data)
	if err != nil || usage.PromptTokens != 3 || usage.CompletionTokens != 5 || usage.TotalTokens != 8 {
		t.Fatalf("native stream usage=%+v err=%v", usage, err)
	}
}
