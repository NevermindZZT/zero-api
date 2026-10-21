package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagementClientFetchCredentialQuotaPreservesCPAResult(t *testing.T) {
	quotaResult := []byte(`{"subscription":{"plan":"Google AI Pro"},"groups":[{"displayName":"Gemini Models","buckets":[{"window":"5h","remainingFraction":0.72,"resetTime":"2026-10-21T00:00:00Z","futureField":"preserved"}]}],"summary":[{"key":"credits","label":"Credits","value":42,"unit":"units"}],"futureQuotaShape":{"enabled":true}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v0/management/quota/fetch" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer management-key" {
			t.Fatalf("unexpected authorization header")
		}
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["auth_index"] != "gemini-auth-1" || request["provider"] != "antigravity" {
			t.Fatalf("unexpected request body: %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(quotaResult)
	}))
	defer server.Close()

	client := &ManagementClient{BaseURL: server.URL, Key: "management-key", HTTP: server.Client()}
	got, err := client.FetchCredentialQuota(context.Background(), AuthFile{AuthIndex: "gemini-auth-1", Provider: "antigravity"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, quotaResult) {
		t.Fatalf("CPA quota response changed: got %s, want %s", got, quotaResult)
	}
}

func TestManagementClientFetchCredentialQuotaPreservesHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"quota provider unavailable"}`, http.StatusNotImplemented)
	}))
	defer server.Close()

	client := &ManagementClient{BaseURL: server.URL, Key: "management-key", HTTP: server.Client()}
	_, err := client.FetchCredentialQuota(context.Background(), AuthFile{AuthIndex: "auth-1", Provider: "antigravity"})
	var apiErr *managementHTTPError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotImplemented {
		t.Fatalf("FetchCredentialQuota error = %v, want management HTTP 501 error", err)
	}
}
