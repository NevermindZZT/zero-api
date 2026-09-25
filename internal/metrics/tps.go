package metrics

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	TPSRetention         = 15 * time.Minute
	TPSMinSampleDuration = 100 * time.Millisecond
	maxModels            = 256
	maxSamplesPerModel   = 256
)

// TPSSampleObserver reads provider-reported output usage and identifies the
// first meaningful output delta without retaining prompt or response content.
type TPSSampleObserver struct {
	FirstOutputAt time.Time
	OutputTokens  int
}

// ObserveSSELine inspects one SSE line. It supports OpenAI Chat Completions,
// Anthropic Messages, OpenAI Responses, and Gemini event payloads.
func (o *TPSSampleObserver) ObserveSSELine(line []byte, at time.Time) {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(trimmed[len("data:"):])
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}

	var event map[string]json.RawMessage
	if json.Unmarshal(payload, &event) != nil {
		return
	}

	if outputDeltaPresent(event) && o.FirstOutputAt.IsZero() {
		o.FirstOutputAt = at
	}
	if tokens := outputTokens(event); tokens > 0 {
		o.OutputTokens = tokens
	}
}

func outputDeltaPresent(event map[string]json.RawMessage) bool {
	var choices []struct {
		Delta map[string]json.RawMessage `json:"delta"`
	}
	if raw := event["choices"]; len(raw) > 0 {
		if json.Unmarshal(raw, &choices) == nil {
			for _, choice := range choices {
				for _, key := range []string{"content", "reasoning_content", "reasoning", "refusal", "audio", "text", "tool_calls", "function_call"} {
					if rawHasOutput(choice.Delta[key]) {
						return true
					}
				}
			}
		}
	}

	var eventType string
	_ = json.Unmarshal(event["type"], &eventType)
	if eventType == "content_block_delta" {
		var delta map[string]json.RawMessage
		if json.Unmarshal(event["delta"], &delta) == nil {
			for _, key := range []string{"text", "thinking", "partial_json", "signature"} {
				if rawHasOutput(delta[key]) {
					return true
				}
			}
		}
	}
	if strings.HasSuffix(eventType, ".delta") && rawHasOutput(event["delta"]) {
		return true
	}

	// Gemini streams carry text in candidates[].content.parts[].text.
	var candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	}
	if json.Unmarshal(event["candidates"], &candidates) == nil {
		for _, candidate := range candidates {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					return true
				}
			}
		}
	}
	return false
}

func rawHasOutput(raw json.RawMessage) bool {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text != ""
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) == nil {
		return len(values) > 0
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		return len(object) > 0
	}
	return false
}

func outputTokens(event map[string]json.RawMessage) int {
	readUsage := func(raw json.RawMessage) int {
		var usage struct {
			CompletionTokens int `json:"completion_tokens"`
			OutputTokens     int `json:"output_tokens"`
		}
		if json.Unmarshal(raw, &usage) != nil {
			return 0
		}
		if usage.CompletionTokens > 0 {
			return usage.CompletionTokens
		}
		return usage.OutputTokens
	}
	if tokens := readUsage(event["usage"]); tokens > 0 {
		return tokens
	}
	var response struct {
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(event["response"], &response) == nil {
		if tokens := readUsage(response.Usage); tokens > 0 {
			return tokens
		}
	}
	var metadata struct {
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	}
	if json.Unmarshal(event["usageMetadata"], &metadata) == nil {
		return metadata.CandidatesTokenCount
	}
	return 0
}

// TPSModelStats is the rolling aggregate returned to the dashboard.
type TPSModelStats struct {
	Model        string    `json:"model"`
	AvgTPS       float64   `json:"avg_tps"`
	SampleCount  int       `json:"sample_count"`
	OutputTokens int64     `json:"output_tokens"`
	GenerationMS int64     `json:"generation_ms"`
	LastAt       time.Time `json:"last_at"`
}

type tpsSample struct {
	completedAt time.Time
	tokens      int
	generation  time.Duration
}

// TPSMonitor keeps a bounded, process-local rolling set of completed stream
// samples. It intentionally has no persistence or request/response content.
type TPSMonitor struct {
	mu     sync.Mutex
	models map[string][]tpsSample
}

func NewTPSMonitor() *TPSMonitor {
	return &TPSMonitor{models: make(map[string][]tpsSample)}
}

// Record adds one valid completed-stream sample. Samples shorter than 100ms
// are ignored because their measured rate is too sensitive to timing noise.
func (m *TPSMonitor) Record(model string, outputTokens int, firstOutputAt, completedAt time.Time) bool {
	if m == nil || strings.TrimSpace(model) == "" || outputTokens <= 0 || firstOutputAt.IsZero() || completedAt.IsZero() {
		return false
	}
	generation := completedAt.Sub(firstOutputAt)
	if generation < TPSMinSampleDuration {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	samples, exists := m.models[model]
	samples = pruneSamples(samples, completedAt.Add(-TPSRetention))
	if len(samples) == 0 && exists {
		delete(m.models, model)
		exists = false
	}
	if !exists && len(m.models) >= maxModels {
		m.pruneLocked(completedAt.Add(-TPSRetention))
		if len(m.models) >= maxModels {
			m.evictOldestModelLocked()
		}
	}
	samples = append(samples, tpsSample{completedAt: completedAt, tokens: outputTokens, generation: generation})
	if len(samples) > 1 && samples[len(samples)-2].completedAt.After(completedAt) {
		sort.Slice(samples, func(i, j int) bool { return samples[i].completedAt.Before(samples[j].completedAt) })
	}
	if len(samples) > maxSamplesPerModel {
		samples = append([]tpsSample(nil), samples[len(samples)-maxSamplesPerModel:]...)
	}
	m.models[model] = samples
	return true
}

// Snapshot returns at most limit models, sorted by most recent activity.
func (m *TPSMonitor) Snapshot(window time.Duration, limit int) []TPSModelStats {
	return m.SnapshotAt(window, limit, time.Now())
}

// SnapshotAt is Snapshot with an explicit clock, useful for deterministic tests.
func (m *TPSMonitor) SnapshotAt(window time.Duration, limit int, now time.Time) []TPSModelStats {
	if m == nil {
		return []TPSModelStats{}
	}
	if window <= 0 || window > TPSRetention {
		window = 5 * time.Minute
	}
	if limit <= 0 {
		limit = 10
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked(now.Add(-TPSRetention))
	cutoff := now.Add(-window)
	stats := make([]TPSModelStats, 0, len(m.models))
	for model, samples := range m.models {
		var item TPSModelStats
		item.Model = model
		for _, sample := range samples {
			if sample.completedAt.Before(cutoff) || sample.completedAt.After(now) {
				continue
			}
			item.SampleCount++
			item.OutputTokens += int64(sample.tokens)
			item.GenerationMS += sample.generation.Milliseconds()
			if sample.completedAt.After(item.LastAt) {
				item.LastAt = sample.completedAt
			}
		}
		if item.SampleCount == 0 || item.GenerationMS <= 0 {
			continue
		}
		item.AvgTPS = float64(item.OutputTokens) * 1000 / float64(item.GenerationMS)
		stats = append(stats, item)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].LastAt.Equal(stats[j].LastAt) {
			return stats[i].Model < stats[j].Model
		}
		return stats[i].LastAt.After(stats[j].LastAt)
	})
	if len(stats) > limit {
		stats = stats[:limit]
	}
	return stats
}

func (m *TPSMonitor) pruneLocked(cutoff time.Time) {
	for model, samples := range m.models {
		kept := pruneSamples(samples, cutoff)
		if len(kept) == 0 {
			delete(m.models, model)
		} else {
			m.models[model] = kept
		}
	}
}

func pruneSamples(samples []tpsSample, cutoff time.Time) []tpsSample {
	kept := samples[:0]
	for _, sample := range samples {
		if !sample.completedAt.Before(cutoff) {
			kept = append(kept, sample)
		}
	}
	return kept
}

func (m *TPSMonitor) evictOldestModelLocked() {
	var oldestModel string
	var oldestAt time.Time
	for model, samples := range m.models {
		if len(samples) == 0 {
			delete(m.models, model)
			continue
		}
		lastAt := samples[len(samples)-1].completedAt
		if oldestModel == "" || lastAt.Before(oldestAt) {
			oldestModel, oldestAt = model, lastAt
		}
	}
	if oldestModel != "" {
		delete(m.models, oldestModel)
	}
}
