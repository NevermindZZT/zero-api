package handler

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/adapter"
	"github.com/never/zero-api/internal/metrics"
	"github.com/never/zero-api/internal/store"
)

const maxSSEFrameBytes = 4 << 20
const maxStreamCaptureBytes = 8 << 20

var errSSEFrameTooLarge = errors.New("SSE frame exceeds limit")

// sseFrame preserves raw bytes for native passthrough, and aggregates data lines
// for protocol converters. SSE dispatch happens on a blank line, not per read.
type sseFrame struct {
	raw   bytes.Buffer
	data  []string
	event string
}

func (f *sseFrame) add(line []byte) (bool, error) {
	if f.raw.Len()+len(line) > maxSSEFrameBytes {
		return false, errSSEFrameTooLarge
	}
	f.raw.Write(line)
	text := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
	if text == "" {
		return true, nil
	}
	if strings.HasPrefix(text, ":") {
		return false, nil
	}
	name, value, found := strings.Cut(text, ":")
	if !found {
		value = ""
	}
	value = strings.TrimPrefix(value, " ")
	switch name {
	case "data":
		f.data = append(f.data, value)
	case "event":
		f.event = value
	}
	return false, nil
}
func (f *sseFrame) payload() []byte { return []byte(strings.Join(f.data, "\n")) }
func (f *sseFrame) reset()          { f.raw.Reset(); f.data = nil; f.event = "" }
func normalizedSSEDataFrame(payload []byte) []byte {
	var compact bytes.Buffer
	if json.Compact(&compact, payload) == nil {
		payload = compact.Bytes()
	}
	return append(append([]byte("data: "), payload...), []byte("\n\n")...)
}

type streamActivityReader struct {
	io.Reader
	activity *atomic.Int64
}

func (r streamActivityReader) Read(data []byte) (int, error) {
	n, err := r.Reader.Read(data)
	if n > 0 {
		r.activity.Store(time.Now().UnixNano())
	}
	return n, err
}

func readSSELine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > maxSSEFrameBytes {
			return nil, errSSEFrameTooLarge
		}
		line = append(line, part...)
		if err != bufio.ErrBufferFull {
			return line, err
		}
	}
}

type streamTerminal struct {
	complete, incomplete, failed bool
	stopNow                      bool
	length                       bool
}

func (t *streamTerminal) observe(protocol, event string, payload []byte) {
	t.stopNow = false
	if event == "error" {
		t.failed = true
		t.complete = true
		t.stopNow = true
		return
	}
	if (protocol == "openai" || protocol == "openrouter") && bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
		t.complete = true
		t.incomplete = t.length
		t.stopNow = true
		return
	}
	if len(payload) == 0 {
		return
	}
	var msg struct {
		Type     string          `json:"type"`
		Error    json.RawMessage `json:"error"`
		Response struct {
			Status string `json:"status"`
		} `json:"response"`
		Choices []struct {
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Candidates []struct {
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if json.Unmarshal(payload, &msg) != nil {
		return
	}
	if len(msg.Error) > 0 && !bytes.Equal(bytes.TrimSpace(msg.Error), []byte("null")) {
		t.failed = true
		t.complete = true
		t.stopNow = true
		return
	}
	if msg.Type == "" {
		msg.Type = event
	}
	switch msg.Type {
	case "error", "response.failed", "response.error":
		t.failed = true
		t.complete = true
		t.stopNow = true
		return
	case "response.incomplete":
		t.incomplete = true
		t.complete = true
		t.stopNow = true
		return
	case "response.completed":
		t.complete = true
		t.stopNow = true
		t.failed = msg.Response.Status == "failed" || msg.Response.Status == "cancelled"
		t.incomplete = msg.Response.Status == "incomplete"
		return
	case "message_delta":
		if msg.Delta.StopReason == "max_tokens" {
			t.length = true
		}
	case "message_stop":
		t.complete = true
		t.incomplete = t.length
		t.stopNow = true
		return
	}
	switch protocol {
	case "openai", "openrouter":
		for _, choice := range msg.Choices {
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				if *choice.FinishReason == "error" {
					t.failed = true
					t.stopNow = true
				}
				t.complete = true
				t.length = t.length || *choice.FinishReason == "length" || *choice.FinishReason == "content_filter"
				t.incomplete = t.length
			}
		}
	case "gemini":
		for _, candidate := range msg.Candidates {
			if candidate.FinishReason != "" {
				t.complete = true
				t.stopNow = true
				switch candidate.FinishReason {
				case "STOP":
				case "MAX_TOKENS", "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY":
					t.incomplete = true
				default:
					t.failed = true
				}
			}
		}
	}
}

// Keep bounded head/tail windows for usage extraction; never retain an unlimited
// stream (e.g. a provider sending heartbeats forever).
type streamCapture struct {
	head, tail bytes.Buffer
	trimmed    bool
}

func (b *streamCapture) write(line []byte) {
	if b.head.Len() < 64<<10 && b.head.Len()+len(line) <= 64<<10 {
		b.head.Write(line)
	}
	if b.tail.Len()+len(line) > maxStreamCaptureBytes {
		old := b.tail.Bytes()
		start := len(old) / 2
		if n := bytes.IndexByte(old[start:], '\n'); n >= 0 {
			start += n + 1
		}
		keep := append([]byte(nil), old[start:]...)
		b.tail.Reset()
		b.tail.Write(keep)
		b.trimmed = true
	}
	b.tail.Write(line)
}
func (b *streamCapture) bytes() []byte {
	if !b.trimmed {
		return b.tail.Bytes()
	}
	result := make([]byte, 0, b.head.Len()+b.tail.Len())
	result = append(result, b.head.Bytes()...)
	return append(result, b.tail.Bytes()...)
}

// Converters can discover semantic failures (e.g. missing tool identity)
// even when the provider sent a nominally successful terminal event.
func convertedStreamFailed(data []byte) bool {
	var frame sseFrame
	failed := func() bool {
		var terminal streamTerminal
		terminal.observe("openai", frame.event, frame.payload())
		if terminal.failed {
			return true
		}
		var value struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		_ = json.Unmarshal(frame.payload(), &value)
		for _, choice := range value.Choices {
			if choice.FinishReason == "error" {
				return true
			}
		}
		return false
	}
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		dispatched, err := frame.add(line)
		if err != nil {
			return true
		}
		if dispatched {
			if failed() {
				return true
			}
			frame.reset()
		}
	}
	return len(frame.data) > 0 && failed()
}

// Existing usage adapters accept canonical JSON, but Anthropic/Gemini native
// SSE is not JSON. Normalize their documented counters without replaying or
// retaining response state outside this one request.
func streamUsageJSON(raw []byte, usageAdapter adapter.Adapter, protocol string) []byte {
	if usage, err := usageAdapter.ExtractUsage(raw); err == nil {
		data, _ := json.Marshal(map[string]any{"usage": usage})
		return data
	}
	if protocol != "anthropic" && protocol != "gemini" {
		return nil
	}
	usage := &adapter.Usage{}
	found := false
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		var event struct {
			Message struct {
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage *struct {
				InputTokens  *int `json:"input_tokens"`
				OutputTokens *int `json:"output_tokens"`
			} `json:"usage"`
			Metadata *struct {
				Prompt int `json:"promptTokenCount"`
				Output int `json:"candidatesTokenCount"`
				Total  int `json:"totalTokenCount"`
			} `json:"usageMetadata"`
		}
		if json.Unmarshal(bytes.TrimPrefix(line, []byte("data: ")), &event) != nil {
			continue
		}
		if event.Message.Usage != nil {
			usage.PromptTokens = event.Message.Usage.InputTokens
			usage.CompletionTokens = event.Message.Usage.OutputTokens
			found = true
		}
		if event.Usage != nil {
			if event.Usage.InputTokens != nil {
				usage.PromptTokens = *event.Usage.InputTokens
			}
			if event.Usage.OutputTokens != nil {
				usage.CompletionTokens = *event.Usage.OutputTokens
			}
			found = true
		}
		if event.Metadata != nil {
			usage.PromptTokens = event.Metadata.Prompt
			usage.CompletionTokens = event.Metadata.Output
			usage.TotalTokens = event.Metadata.Total
			found = true
		}
	}
	if !found {
		return nil
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	data, _ := json.Marshal(map[string]any{"usage": usage})
	return data
}

func streamErrorPayload(protocol, category string) []byte {
	message := "Upstream stream did not complete"
	var payload any
	event := ""
	switch protocol {
	case "anthropic":
		event = "error"
		payload = map[string]any{"type": "error", "error": map[string]string{"type": "api_error", "message": message}}
	case "responses":
		event = "error"
		payload = map[string]string{"type": "error", "code": category, "message": message}
	default:
		payload = map[string]any{"error": map[string]string{"type": category, "message": message}}
	}
	data, _ := json.Marshal(payload)
	if event != "" {
		return append(append([]byte("event: "+event+"\ndata: "), data...), []byte("\n\n")...)
	}
	return append(append([]byte("data: "), data...), []byte("\n\n")...)
}

func (h *ProxyHandler) forwardSSE(c *gin.Context, resp *http.Response, adapt adapter.Adapter, model *store.Model, ch *store.Channel, apiKeyID *int64, start time.Time, timeout time.Duration, downstream adapter.DownstreamAdapter) (result error) {
	timeout = forwardTimeout(timeout)
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetWriteDeadline(time.Time{})
	for name, values := range filterHopByHop(resp.Header) {
		if strings.EqualFold(name, "X-Request-Id") {
			continue
		}
		if strings.EqualFold(name, "Content-Length") || strings.EqualFold(name, "Content-Type") {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(name, value)
		}
	}
	c.Writer.Header().Del("Content-Length")
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Status(resp.StatusCode)

	var idleExpired atomic.Bool
	var activity atomic.Int64
	activity.Store(time.Now().UnixNano())
	done := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-c.Request.Context().Done():
				_ = controller.SetWriteDeadline(time.Now())
				_ = resp.Body.Close()
				return
			case <-timer.C:
				remaining := timeout - time.Since(time.Unix(0, activity.Load()))
				if remaining > 0 {
					timer.Reset(remaining)
					continue
				}
				idleExpired.Store(true)
				_ = controller.SetWriteDeadline(time.Now())
				_ = resp.Body.Close()
				return
			}
		}
	}()
	defer func() { close(done); <-watcherDone }()
	write := func(data []byte) error {
		if len(data) == 0 {
			return nil
		}
		if c.Request.Context().Err() != nil {
			return &forwardError{category: "client_cancelled", clientCancelled: true, cause: c.Request.Context().Err()}
		}
		_ = controller.SetWriteDeadline(time.Now().Add(timeout))
		if _, err := c.Writer.Write(data); err != nil {
			if c.Request.Context().Err() != nil {
				return &forwardError{category: "client_cancelled", clientCancelled: true, cause: err}
			}
			if idleExpired.Load() {
				return forwardingFailure("stream_idle_timeout", err, false, false)
			}
			return forwardingFailure("downstream_write", err, false, false)
		}
		if err := controller.Flush(); err != nil {
			if c.Request.Context().Err() != nil {
				return &forwardError{category: "client_cancelled", clientCancelled: true, cause: err}
			}
			return forwardingFailure("downstream_write", err, false, false)
		}
		return nil
	}
	// Protocol errors after commitment are observable, but must never cause a
	// second request/channel replay. Native provider error frames remain native.
	defer func() {
		if result == nil {
			return
		}
		outcome := forwardOutcome(result, c.Writer.Written())
		if outcome.started && !outcome.streamReported && !outcome.clientCancelled && outcome.category != "downstream_write" && outcome.category != "stream_incomplete" && outcome.category != "stream_failed" {
			_ = write(streamErrorPayload(downstream.Protocol(), outcome.category))
		}
		outcome.streamReported = true
		result = outcome
	}()
	var upstreamConverter adapter.StreamConverter
	if !downstream.IsPassthrough() {
		upstreamConverter = adapt.NewStreamConverter()
	}
	downstreamConverter := downstream.NewStreamConverter()
	var convertedTerminal streamTerminal
	var convertedFrame sseFrame
	writeConverted := func(out []byte) error {
		failed := convertedStreamFailed(out)
		if err := write(out); err != nil {
			return err
		}
		if failed {
			return &forwardError{category: "conversion_error", status: http.StatusBadGateway, streamReported: true}
		}
		for _, line := range bytes.SplitAfter(out, []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			dispatched, err := convertedFrame.add(line)
			if err != nil {
				return &forwardError{category: "conversion_error", status: http.StatusBadGateway}
			}
			if dispatched {
				convertedTerminal.observe(downstream.Protocol(), convertedFrame.event, convertedFrame.payload())
				convertedFrame.reset()
			}
		}
		return nil
	}
	convertCanonical := func(data []byte) error {
		failed := convertedStreamFailed(data)
		for _, line := range bytes.SplitAfter(data, []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			if err := writeConverted(downstreamConverter.Convert(line)); err != nil {
				return err
			}
		}
		if failed {
			if err := write(streamErrorPayload(downstream.Protocol(), "conversion_error")); err != nil {
				return err
			}
			return &forwardError{category: "conversion_error", status: http.StatusBadGateway, streamReported: true}
		}
		return nil
	}
	protocol := ch.Type
	if downstream.IsPassthrough() {
		protocol = downstream.Protocol()
	}
	var terminal streamTerminal
	var frame sseFrame
	var capture streamCapture
	var observer metrics.TPSSampleObserver
	reader := bufio.NewReaderSize(streamActivityReader{Reader: resp.Body, activity: &activity}, 64<<10)
	for {
		line, readErr := readSSELine(reader)
		if len(line) > 0 {
			activity.Store(time.Now().UnixNano())
			dispatched, err := frame.add(line)
			if err != nil {
				return forwardingFailure("stream_truncated", err, true, true)
			}
			if dispatched {
				payload := frame.payload()
				var normalized []byte
				if len(frame.data) > 0 {
					normalized = normalizedSSEDataFrame(payload)
					capture.write(normalized)
					observer.ObserveSSELine(normalized, time.Now())
				}
				terminal.observe(protocol, frame.event, payload)
				if terminal.failed {
					if downstream.IsPassthrough() {
						if err := write(frame.raw.Bytes()); err != nil {
							return err
						}
					} else {
						if err := write(streamErrorPayload(downstream.Protocol(), "stream_failed")); err != nil {
							return err
						}
					}
					outcome := forwardingFailure("stream_failed", nil, false, true)
					outcome.upstreamErrorCode = providerErrorCode(payload, 200)
					switch outcome.upstreamErrorCode {
					case "context_length_exceeded", "previous_response_not_found", "tool_call_mismatch", "invalid_request":
						outcome.breakerFailure = false
					}
					return outcome
				}
				if downstream.IsPassthrough() {
					if err := write(frame.raw.Bytes()); err != nil {
						return err
					}
				} else if len(frame.data) > 0 {
					canonical := normalized
					if upstreamConverter != nil {
						canonical = upstreamConverter.Convert(canonical)
					}
					if err := convertCanonical(canonical); err != nil {
						return err
					}
				} else if upstreamConverter == nil {
					// Comments are harmless keepalives. Converters may intentionally drop them.
					if err := convertCanonical(frame.raw.Bytes()); err != nil {
						return err
					}
				}
				frame.reset()
				if terminal.stopNow {
					break
				}
			}
		}
		if readErr != nil {
			if c.Request.Context().Err() != nil {
				return &forwardError{category: "client_cancelled", clientCancelled: true, cause: readErr}
			}
			if terminal.complete && !terminal.failed {
				break
			}
			if idleExpired.Load() {
				return forwardingFailure("stream_idle_timeout", readErr, true, true)
			}
			return forwardingFailure("stream_truncated", readErr, true, true)
		}
	}
	if !terminal.complete {
		return forwardingFailure("stream_truncated", io.ErrUnexpectedEOF, true, true)
	}
	if upstreamConverter != nil {
		if err := convertCanonical(upstreamConverter.Finish()); err != nil {
			return err
		}
	}
	if err := writeConverted(downstreamConverter.Finish()); err != nil {
		return err
	}
	if !downstream.IsPassthrough() && !convertedTerminal.complete {
		return &forwardError{category: "conversion_error", status: http.StatusBadGateway}
	}
	completed := time.Now()
	latency := int(completed.Sub(start).Milliseconds())
	if !observer.FirstOutputAt.IsZero() {
		latency = int(observer.FirstOutputAt.Sub(start).Milliseconds())
	}
	h.recordStreamTPS(model, observer, completed)
	raw := capture.bytes()
	if len(raw) > 0 && h.usageRepo != nil {
		usageAdapter := adapter.SelectUsageAdapter(adapt, downstream.IsPassthrough(), downstream.Protocol())
		converted := streamUsageJSON(raw, usageAdapter, protocol)
		if len(converted) == 0 {
			converted, _ = adapt.ConvertResponse(raw)
		}
		go h.recordUsage(model.ModelID, raw, converted, adapt, usageAdapter, model, ch.ID, apiKeyID, latency, int(completed.Sub(start).Milliseconds()))
	}
	if terminal.incomplete {
		return &forwardError{category: "stream_incomplete", status: resp.StatusCode, upstreamStatus: resp.StatusCode, started: c.Writer.Written()}
	}
	return nil
}
