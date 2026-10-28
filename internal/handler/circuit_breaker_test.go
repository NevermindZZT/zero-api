package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/never/zero-api/internal/store"
)

func TestCircuitBreakerSingleHalfOpenProbe(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.baseWait = time.Hour
	cb.maxWait = 8 * time.Hour
	cb.RecordFailure(1)
	if allow, probe := cb.MayProceed(1); allow || probe {
		t.Fatal("cooldown allowed traffic")
	}
	cb.mu.Lock()
	cb.entries[1].expiry = time.Now().Add(-time.Second)
	cb.mu.Unlock()
	var probes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allow, probe := cb.MayProceed(1)
			if allow {
				t.Error("half-open allowed normal request")
			}
			if probe {
				probes.Add(1)
			}
		}()
	}
	wg.Wait()
	if probes.Load() != 1 {
		t.Fatalf("concurrent probes=%d", probes.Load())
	}
	cb.CancelProbe(1)
	if _, probe := cb.MayProceed(1); !probe {
		t.Fatal("cancelled probe reservation not released")
	}
	cb.RecordFailure(1)
	cb.mu.RLock()
	retries := cb.entries[1].retries
	cb.mu.RUnlock()
	if retries != 2 {
		t.Fatalf("retries=%d", retries)
	}
	cb.RecordSuccess(1)
	if allow, probe := cb.MayProceed(1); !allow || probe {
		t.Fatal("successful channel not recovered")
	}
}

func TestProbeRejectsNonSuccessAndInvalidSuccess(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500, 503, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"provider-secret"}}`)
			}))
			defer server.Close()
			cb := NewCircuitBreaker()
			cb.RecordFailure(1)
			err := cb.ProbeAndRecover(1, &store.Channel{ID: 1, Type: "openai", BaseURL: server.URL}, "test", "", "", "", "", time.Second)
			if err == nil {
				t.Fatalf("probe accepted status %d", status)
			}
			if strings.Contains(err.Error(), "provider-secret") {
				t.Fatal("provider text leaked in error")
			}
			if allow, _ := cb.MayProceed(1); allow {
				t.Fatal("unhealthy channel marked healthy")
			}
		})
	}
}

func TestProbeUsesTargetProtocolCredentialAndModelURL(t *testing.T) {
	cases := []struct{ protocol, path, body string }{
		{"openai", "/v1/chat/completions", `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`},
		{"responses", "/custom-responses", "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"},
		{"anthropic", "/v1/messages", `{"type":"message","content":[{"type":"text","text":"hi"}]}`},
		{"gemini", "/v1beta/models/test:generateContent", `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.protocol, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("probe path=%q want%q", r.URL.Path, tc.path)
				}
				var root map[string]interface{}
				if err := json.NewDecoder(r.Body).Decode(&root); err != nil {
					t.Error(err)
				}
				switch tc.protocol {
				case "responses":
					if _, ok := root["input"]; !ok {
						t.Error("missing Responses input")
					}
					if _, ok := root["messages"]; ok {
						t.Error("Chat payload sent to Responses")
					}
				case "anthropic":
					if root["messages"] == nil || r.Header.Get("anthropic-version") == "" {
						t.Error("invalid Anthropic request")
					}
				case "gemini":
					if root["contents"] == nil {
						t.Error("missing Gemini contents")
					}
				}
				key := r.Header.Get("Authorization")
				if tc.protocol == "anthropic" {
					key = r.Header.Get("x-api-key")
				} else if tc.protocol == "gemini" {
					key = r.Header.Get("x-goog-api-key")
				}
				if !strings.Contains(key, "channel-test-key") || strings.Contains(key, "wrong-probe-key") {
					t.Error("channel credential not preferred")
				}
				if tc.protocol == "responses" {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Type", "application/json")
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			ch := &store.Channel{ID: 1, Type: tc.protocol, BaseURL: server.URL, APIKey: "channel-test-key"}
			m := &store.Model{ModelID: "test"}
			if tc.protocol == "responses" {
				m.ProtocolURLs = map[string]string{"responses": server.URL + tc.path}
			}
			cb := NewCircuitBreaker()
			if err := cb.ProbeModelAndRecoverContext(context.Background(), ch, m, tc.protocol, "wrong-probe-key", "", "", "", time.Second); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProbeCancellationDoesNotPoisonChannel(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.RecordFailure(1)
	cb.mu.Lock()
	cb.entries[1].state = breakerProbing
	cb.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cb.ProbeAndRecoverContext(ctx, 1, &store.Channel{ID: 1, Type: "responses", BaseURL: "http://127.0.0.1:1"}, "test", "", "", "", "", time.Second)
	if err == nil {
		t.Fatal("cancelled probe succeeded")
	}
	cb.mu.RLock()
	retries := cb.entries[1].retries
	state := cb.entries[1].state
	cb.mu.RUnlock()
	if retries != 1 || state != breakerCooldown {
		t.Fatalf("cancel incorrectly penalized channel: retries=%d state=%v", retries, state)
	}
}

func TestProbeRequiresTerminalEventAndRejectsFailure(t *testing.T) {
	for _, body := range []string{": ping\n\n", "data: {\"type\":\"response.created\"}\n\n", "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n"} {
		if validProbeResponse(strings.NewReader(body), "text/event-stream", "responses") {
			t.Fatal("nonterminal/failed stream considered healthy")
		}
	}
}
