package cpa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthFileFromMapSupportsCurrentAndLegacyFields(t *testing.T) {
	file := authFileFromMap(map[string]any{
		"id": "ag.json", "name": "ag.json", "authIndex": "idx-1", "type": "antigravity",
		"account_type": "oauth", "account": "user@example.com", "project_id": "aicode-consumers",
		"status": "active", "status_message": "ok", "disabled": false, "unavailable": false,
	})
	if file.Provider != "antigravity" || file.AuthIndex != "idx-1" || file.ProjectID != "aicode-consumers" {
		t.Fatalf("unexpected auth file: %#v", file)
	}
	if file.AccountID != "user@example.com" || file.Name != "ag.json" || file.StatusMessage != "ok" {
		t.Fatalf("unexpected auth metadata: %#v", file)
	}
}

func TestManagementClientCallUpstreamPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/api-call" || r.Header.Get("Authorization") != "Bearer management-key" {
			t.Fatalf("unexpected management request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["auth_index"] != "idx-1" || payload["method"] != "GET" || payload["url"] != "https://chatgpt.com/backend-api/wham/usage" {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		headers := payload["header"].(map[string]any)
		if headers["Authorization"] != "Bearer $TOKEN$" || headers["User-Agent"] == nil {
			t.Fatalf("unexpected upstream headers: %+v", headers)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"rate_limit\":{\"primary_window\":{\"used_percent\":1}}}"}`))
	}))
	defer server.Close()

	client := &ManagementClient{BaseURL: server.URL, Key: "management-key", HTTP: server.Client()}
	body, status, err := client.CallUpstream(context.Background(), "idx-1", "GET", "https://chatgpt.com/backend-api/wham/usage", map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"User-Agent":    "codex_cli_rs/test",
	})
	if err != nil || status != http.StatusOK || string(body) == "" {
		t.Fatalf("CallUpstream = status %d body %q err %v", status, body, err)
	}
}
