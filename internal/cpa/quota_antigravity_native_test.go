package cpa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAntigravityQuotaProviderUsesNativePanelUserAgentAndPreservesJSON(t *testing.T) {
	quotaBody := `{"groups":[{"displayName":"Gemini Models","buckets":[{"bucketId":"gemini-5h","window":"5h","remainingFraction":0.99}]},{"displayName":"Claude and GPT models","buckets":[{"bucketId":"3p-5h","window":"5h","remainingFraction":1}]}],"futureQuotaMetadata":{"source":"cpa-native"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v0/management/api-call" {
			t.Fatalf("unexpected management request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer management-key" {
			t.Fatal("unexpected Management API authorization")
		}
		var payload struct {
			AuthIndex string            `json:"auth_index"`
			Method    string            `json:"method"`
			URL       string            `json:"url"`
			Header    map[string]string `json:"header"`
			Data      string            `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.AuthIndex != "antigravity-auth-1" || payload.Method != http.MethodPost || payload.URL != antigravityQuotaURLs[0] {
			t.Fatalf("unexpected upstream call: %#v", payload)
		}
		if got := payload.Header["User-Agent"]; got != "antigravity/hub/2.23.0 darwin/arm64" {
			t.Fatalf("User-Agent = %q, want native panel value", got)
		}
		if payload.Data != `{"project":"aicode-consumers"}` {
			t.Fatalf("request data = %q", payload.Data)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status_code": http.StatusOK, "body": quotaBody})
	}))
	defer server.Close()

	client := &ManagementClient{BaseURL: server.URL, Key: "management-key", HTTP: server.Client()}
	got, err := (AntigravityQuotaProvider{}).Query(context.Background(), client, AuthFile{
		AuthIndex: "antigravity-auth-1", Provider: "antigravity", ProjectID: "aicode-consumers",
		Name: "antigravity.json", Email: "user@example.com", PlanType: "pro",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "available" || got.QuotaSource != "cpa-api-call" || got.CredentialName != "antigravity.json" {
		t.Fatalf("unexpected quota snapshot metadata: %#v", got)
	}
	var result map[string]any
	if err := json.Unmarshal(got.CPAResult, &result); err != nil {
		t.Fatalf("CPA quota result is invalid JSON: %v", err)
	}
	groups, ok := result["groups"].([]any)
	if !ok || len(groups) != 2 {
		t.Fatalf("expected both Gemini and Claude/GPT groups in preserved result: %s", got.CPAResult)
	}
	if _, ok := result["futureQuotaMetadata"]; !ok {
		t.Fatalf("future CPA quota fields were dropped: %s", got.CPAResult)
	}
}
