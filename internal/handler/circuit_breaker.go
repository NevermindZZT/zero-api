package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/never/zero-api/internal/adapter"
	"github.com/never/zero-api/internal/store"
	"github.com/never/zero-api/internal/upstream"
)

type breakerState int

const (
	breakerNormal breakerState = iota
	breakerCooldown
	breakerProbing
)

type channelEntry struct {
	state   breakerState
	expiry  time.Time
	retries int
}

// CircuitBreaker maintains channel cooldowns and allows only one half-open probe.
// A request observing a cooldown must never silently mark the channel healthy.
type CircuitBreaker struct {
	mu       sync.RWMutex
	entries  map[int64]*channelEntry
	baseWait time.Duration
	maxWait  time.Duration
}

func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{entries: make(map[int64]*channelEntry), baseWait: 5 * time.Minute, maxWait: 40 * time.Minute}
}

func (cb *CircuitBreaker) RecordFailure(channelID int64) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	entry := cb.entries[channelID]
	if entry == nil {
		entry = &channelEntry{}
		cb.entries[channelID] = entry
	}
	entry.retries++
	wait := cb.baseWait
	for i := 1; i < entry.retries && wait < cb.maxWait; i++ {
		wait *= 2
	}
	if wait > cb.maxWait {
		wait = cb.maxWait
	}
	entry.state, entry.expiry = breakerCooldown, time.Now().Add(wait)
	log.Printf("[熔断] 渠道 %d 失败，冷却 %v (连续失败 %d 次)", channelID, wait, entry.retries)
}

func (cb *CircuitBreaker) RecordSuccess(channelID int64) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if _, exists := cb.entries[channelID]; exists {
		delete(cb.entries, channelID)
		log.Printf("[熔断] 渠道 %d 已恢复正常", channelID)
	}
}

// MayProceed atomically reserves a half-open probe when its cooldown expires.
func (cb *CircuitBreaker) MayProceed(channelID int64) (allow, probe bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	entry := cb.entries[channelID]
	if entry == nil {
		return true, false
	}
	switch entry.state {
	case breakerCooldown:
		if time.Now().Before(entry.expiry) {
			return false, false
		}
		entry.state = breakerProbing
		return false, true
	case breakerProbing:
		return false, false
	default:
		return true, false
	}
}

// EnterProbing is retained for callers that already hold the MayProceed reservation.
func (cb *CircuitBreaker) EnterProbing(channelID int64) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if entry := cb.entries[channelID]; entry != nil {
		entry.state = breakerProbing
	}
}

// CancelProbe releases a probe cancelled by its caller without penalizing the upstream.
func (cb *CircuitBreaker) CancelProbe(channelID int64) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if entry := cb.entries[channelID]; entry != nil && entry.state == breakerProbing {
		entry.state, entry.expiry = breakerCooldown, time.Now()
	}
}

func (cb *CircuitBreaker) RetryAfter(channelID int64) time.Duration {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	if entry := cb.entries[channelID]; entry != nil {
		if wait := time.Until(entry.expiry); wait > 0 {
			return wait
		}
		return time.Second
	}
	return 0
}

// Probe errors intentionally omit upstream URLs, credentials, and response bodies.
type channelProbeError struct {
	reason string
	cause  error
}

func (e *channelProbeError) Error() string { return e.reason }
func (e *channelProbeError) Unwrap() error { return e.cause }

func (cb *CircuitBreaker) ProbeChannel(ch *store.Channel, testModel, probeAPIKey, proxyURL, proxyUser, proxyPass string, timeout time.Duration) error {
	return cb.probeChannel(context.Background(), ch, testModel, ch.Type, "", probeAPIKey, proxyURL, proxyUser, proxyPass, timeout)
}

func (cb *CircuitBreaker) probeChannel(ctx context.Context, ch *store.Channel, testModel, protocol, overrideURL, probeAPIKey, proxyURL, proxyUser, proxyPass string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Responses/Codex endpoints may be streaming-only. Validate their terminal event,
	// rather than accepting a 200 or a keepalive as evidence of a healthy inference.
	payload := map[string]interface{}{"model": testModel, "messages": []map[string]string{{"role": "user", "content": "hi"}}, "max_tokens": 16, "stream": protocol == "responses"}
	canonical, _ := json.Marshal(payload)
	adapt := adapter.NewAdapter(protocol)
	body, err := adapt.ConvertRequest(testModel, canonical)
	if err != nil {
		return &channelProbeError{reason: "探测请求转换失败", cause: err}
	}
	endpoint := adapt.GetChatURL(ch.BaseURL)
	if overrideURL != "" {
		endpoint = overrideURL
	}
	if protocol == "gemini" {
		endpoint = fmt.Sprintf("%s/%s:generateContent", endpoint, testModel)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return &channelProbeError{reason: "探测请求地址无效", cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	key := ch.APIKey
	if key == "" {
		key = probeAPIKey
	}
	switch protocol {
	case "anthropic":
		req.Header.Set("anthropic-version", "2023-06-01")
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
	case "gemini":
		if key != "" {
			req.Header.Set("x-goog-api-key", key)
		}
	default:
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	client := upstream.NewHTTPClientWithTimeout(timeout)
	if proxyURL != "" {
		client, err = upstream.NewHTTPClientWithProxyAndTimeout(proxyURL, proxyUser, proxyPass, timeout)
		if err != nil {
			return &channelProbeError{reason: "探测代理配置无效", cause: err}
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return &channelProbeError{reason: "探测上游连接失败", cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &channelProbeError{reason: fmt.Sprintf("探测上游 HTTP %d", resp.StatusCode)}
	}
	if !validProbeResponse(resp.Body, resp.Header.Get("Content-Type"), protocol) {
		if ctx.Err() != nil {
			return &channelProbeError{reason: "探测读取已取消或超时", cause: ctx.Err()}
		}
		return &channelProbeError{reason: "探测未返回有效推理结果"}
	}
	return nil
}

func validProbeResponse(body io.Reader, contentType, protocol string) bool {
	const limit = 1 << 20
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		scanner := bufio.NewScanner(io.LimitReader(body, limit))
		scanner.Buffer(make([]byte, 4096), limit)
		sawOutput := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return sawOutput
			}
			var event map[string]json.RawMessage
			if json.Unmarshal([]byte(data), &event) != nil {
				continue
			}
			if _, ok := event["error"]; ok {
				return false
			}
			var typ string
			_ = json.Unmarshal(event["type"], &typ)
			switch typ {
			case "response.failed", "error":
				return false
			case "response.completed", "response.incomplete":
				return validProbeJSON(event["response"], "responses")
			case "message_start":
				sawOutput = true
			case "message_stop":
				return sawOutput
			}
			if choices := event["choices"]; len(choices) > 0 {
				sawOutput = true
			}
		}
		return false
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	return err == nil && len(data) <= limit && validProbeJSON(data, protocol)
}

func validProbeJSON(data []byte, protocol string) bool {
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || len(root) == 0 || len(root["error"]) > 0 {
		return false
	}
	switch protocol {
	case "responses":
		var status string
		_ = json.Unmarshal(root["status"], &status)
		var output []json.RawMessage
		return (status == "completed" || status == "incomplete") && json.Unmarshal(root["output"], &output) == nil && output != nil
	case "anthropic":
		var typ string
		_ = json.Unmarshal(root["type"], &typ)
		return typ == "message" && len(root["content"]) > 0
	case "gemini":
		var candidates []json.RawMessage
		return json.Unmarshal(root["candidates"], &candidates) == nil && len(candidates) > 0
	default:
		var choices []json.RawMessage
		return json.Unmarshal(root["choices"], &choices) == nil && len(choices) > 0
	}
}

func (cb *CircuitBreaker) ProbeAndRecover(channelID int64, ch *store.Channel, testModel, probeAPIKey, proxyURL, proxyUser, proxyPass string, timeout time.Duration) error {
	return cb.ProbeAndRecoverContext(context.Background(), channelID, ch, testModel, probeAPIKey, proxyURL, proxyUser, proxyPass, timeout)
}

func (cb *CircuitBreaker) ProbeAndRecoverContext(ctx context.Context, channelID int64, ch *store.Channel, testModel, probeAPIKey, proxyURL, proxyUser, proxyPass string, timeout time.Duration) error {
	err := cb.probeChannel(ctx, ch, testModel, ch.Type, "", probeAPIKey, proxyURL, proxyUser, proxyPass, timeout)
	return cb.finishProbe(ctx, channelID, err)
}

func (cb *CircuitBreaker) ProbeModelAndRecoverContext(ctx context.Context, ch *store.Channel, model *store.Model, downstreamProtocol, probeAPIKey, proxyURL, proxyUser, proxyPass string, timeout time.Duration) error {
	protocol := ch.Type
	endpoint := ""
	if ch.Type != "gemini" && adapter.CanPassthrough(ch.Type, downstreamProtocol, ch.SupportsProtocol(downstreamProtocol), model.SupportsProtocol(downstreamProtocol, ch.Type)) {
		protocol = downstreamProtocol
		endpoint = model.ProtocolURL(protocol, ch.BaseURL)
	}
	testModel := ch.TestModel
	if testModel == "" {
		testModel = model.ModelID
	}
	err := cb.probeChannel(ctx, ch, testModel, protocol, endpoint, probeAPIKey, proxyURL, proxyUser, proxyPass, timeout)
	return cb.finishProbe(ctx, ch.ID, err)
}

func (cb *CircuitBreaker) finishProbe(ctx context.Context, channelID int64, err error) error {
	if err == nil {
		cb.RecordSuccess(channelID)
		return nil
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		cb.CancelProbe(channelID)
		return err
	}
	cb.RecordFailure(channelID)
	log.Printf("[熔断] 渠道 %d 探测失败", channelID)
	return err
}
