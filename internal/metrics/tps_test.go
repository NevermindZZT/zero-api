package metrics

import (
	"math"
	"testing"
	"time"
)

func TestTPSSampleObserverRecognizesOutputAndUsage(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		lines   []string
		tokens  int
		started time.Time
	}{
		{
			name: "openai",
			lines: []string{
				`data: {"choices":[{"delta":{"role":"assistant"}}]}`,
				`data: {"choices":[{"delta":{"content":"hello"}}]}`,
				`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":7}}`,
			},
			tokens: 7, started: base.Add(time.Second),
		},
		{
			name: "anthropic",
			lines: []string{
				`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}`,
				`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
				`data: {"type":"message_delta","usage":{"output_tokens":9}}`,
			},
			tokens: 9, started: base.Add(time.Second),
		},
		{
			name: "responses",
			lines: []string{
				`data: {"type":"response.created"}`,
				`data: {"type":"response.output_text.delta","delta":"hello"}`,
				`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":11}}}`,
			},
			tokens: 11, started: base.Add(time.Second),
		},
		{
			name: "gemini",
			lines: []string{
				`data: {"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}`,
				`data: {"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":13,"totalTokenCount":23}}`,
			},
			tokens: 13, started: base,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var observer TPSSampleObserver
			for i, line := range tt.lines {
				observer.ObserveSSELine([]byte(line), base.Add(time.Duration(i)*time.Second))
			}
			if observer.OutputTokens != tt.tokens {
				t.Fatalf("OutputTokens = %d, want %d", observer.OutputTokens, tt.tokens)
			}
			if !observer.FirstOutputAt.Equal(tt.started) {
				t.Fatalf("FirstOutputAt = %s, want %s", observer.FirstOutputAt, tt.started)
			}
		})
	}
}

func TestTPSMonitorComputesWeightedRollingAverage(t *testing.T) {
	monitor := NewTPSMonitor()
	now := time.Date(2025, 1, 1, 0, 10, 0, 0, time.UTC)
	if !monitor.Record("model-a", 100, now.Add(-10*time.Second), now.Add(-8*time.Second)) {
		t.Fatal("expected valid first sample")
	}
	if !monitor.Record("model-a", 20, now.Add(-4*time.Second), now.Add(-3*time.Second)) {
		t.Fatal("expected valid second sample")
	}
	if !monitor.Record("model-b", 10, now.Add(-2*time.Second), now.Add(-time.Second)) {
		t.Fatal("expected valid third sample")
	}

	stats := monitor.SnapshotAt(5*time.Minute, 10, now)
	if len(stats) != 2 {
		t.Fatalf("got %d models, want 2", len(stats))
	}
	if stats[0].Model != "model-b" {
		t.Fatalf("most recent model = %q, want model-b", stats[0].Model)
	}
	var modelA TPSModelStats
	for _, item := range stats {
		if item.Model == "model-a" {
			modelA = item
		}
	}
	if modelA.SampleCount != 2 || modelA.OutputTokens != 120 || modelA.GenerationMS != 3000 {
		t.Fatalf("unexpected model-a aggregate: %+v", modelA)
	}
	if math.Abs(modelA.AvgTPS-40) > 0.001 {
		t.Fatalf("model-a AvgTPS = %f, want 40", modelA.AvgTPS)
	}
}

func TestTPSMonitorWindowAndInvalidSamples(t *testing.T) {
	monitor := NewTPSMonitor()
	now := time.Date(2025, 1, 1, 0, 10, 0, 0, time.UTC)
	if monitor.Record("too-fast", 5, now.Add(-time.Second), now.Add(-time.Second+50*time.Millisecond)) {
		t.Fatal("sub-100ms sample must be ignored")
	}
	if monitor.Record("missing-usage", 0, now.Add(-time.Second), now) {
		t.Fatal("zero-token sample must be ignored")
	}
	if !monitor.Record("old", 10, now.Add(-7*time.Minute), now.Add(-6*time.Minute)) {
		t.Fatal("expected valid old sample")
	}
	if !monitor.Record("recent", 10, now.Add(-2*time.Second), now.Add(-time.Second)) {
		t.Fatal("expected valid recent sample")
	}

	stats := monitor.SnapshotAt(time.Minute, 10, now)
	if len(stats) != 1 || stats[0].Model != "recent" {
		t.Fatalf("one-minute snapshot = %+v, want only recent", stats)
	}
	stats = monitor.SnapshotAt(15*time.Minute, 10, now)
	if len(stats) != 2 {
		t.Fatalf("15-minute snapshot contains %d models, want 2", len(stats))
	}
}

func TestTPSMonitorTrimsExpiredSamples(t *testing.T) {
	monitor := NewTPSMonitor()
	now := time.Date(2025, 1, 1, 0, 10, 0, 0, time.UTC)
	if !monitor.Record("model-a", 4, now.Add(-16*time.Minute), now.Add(-15*time.Minute-time.Second)) {
		t.Fatal("expected valid sample")
	}
	if got := monitor.SnapshotAt(15*time.Minute, 10, now); len(got) != 0 {
		t.Fatalf("expired sample remained: %+v", got)
	}
}
