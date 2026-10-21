package cpa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeQuotaProvider struct{}

func (fakeQuotaProvider) ID() string { return "fake" }
func (fakeQuotaProvider) Match(auth AuthFile) bool {
	return auth.AuthIndex != "" && !auth.Disabled && !auth.Unavailable
}
func (fakeQuotaProvider) Query(_ context.Context, _ *ManagementClient, auth AuthFile) (*QuotaSnapshot, error) {
	if auth.AuthIndex == "fail" {
		return nil, errors.New("quota upstream failed")
	}
	return &QuotaSnapshot{Provider: "fake", AuthIndex: auth.AuthIndex, Status: "available"}, nil
}

func TestQuotaServiceReturnsEmptyWithoutCodexAuth(t *testing.T) {
	// 通过假的 Management API 不在本测试中发起网络请求，验证 provider 匹配规则。
	if (CodexQuotaProvider{}).Match(AuthFile{Provider: "codex", AuthIndex: "idx", AccountType: "api_key"}) {
		t.Fatal("Codex API key must not be treated as OAuth subscription")
	}
	if !(CodexQuotaProvider{}).Match(AuthFile{Provider: "codex", AuthIndex: "idx"}) {
		t.Fatal("Codex auth without account_type should remain eligible")
	}
}

func TestQuotaServiceUsesNativeCPAQuotaResultWithoutProviderParser(t *testing.T) {
	quotaResult := `{"groups":[{"displayName":"Gemini Models","buckets":[{"window":"5h","remainingFraction":0.72,"futureBucketField":"visible"}]}],"futureQuotaShape":{"newField":"preserved"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{{"name": "future-provider.json", "provider": "future-provider", "auth_index": "future-auth-1", "status": "active"}}})
		case "/v0/management/quota/fetch":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request["auth_index"] != "future-auth-1" || request["provider"] != "future-provider" {
				t.Fatalf("unexpected CPA quota request: %#v", request)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(quotaResult))
		default:
			t.Fatalf("unexpected management API path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := &ManagementClient{BaseURL: server.URL, Key: "management-key", HTTP: server.Client()}
	service := &QuotaService{client: client, ttl: 5}
	result, err := service.Query(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1: %#v", len(result.Accounts), result.Accounts)
	}
	account := result.Accounts[0]
	if account.Status != "available" || account.QuotaSource != "cpa" || account.Provider != "future-provider" {
		t.Fatalf("unexpected CPA account result: %#v", account)
	}
	if string(account.CPAResult) != quotaResult {
		t.Fatalf("CPA quota JSON was not preserved: %s", account.CPAResult)
	}
}

func TestQuotaServiceKeepsFailedAndUnavailableAccountsVisible(t *testing.T) {
	authFiles := []map[string]any{
		{"name": "ok.json", "provider": "fake", "auth_index": "ok", "email": "ok@example.com"},
		{"name": "fail.json", "provider": "fake", "auth_index": "fail", "email": "fail@example.com"},
		{"name": "disabled.json", "provider": "fake", "auth_index": "disabled", "email": "disabled@example.com", "disabled": true},
		{"name": "missing-index.json", "provider": "fake", "email": "missing@example.com"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_ = json.NewEncoder(w).Encode(map[string]any{"files": authFiles})
		case "/v0/management/quota/fetch":
			http.NotFound(w, r) // 模拟旧版 CPA 未实现原生 quota/fetch 接口。
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &ManagementClient{BaseURL: server.URL, Key: "key", HTTP: server.Client()}
	service := &QuotaService{client: client, providers: []QuotaProvider{fakeQuotaProvider{}}, ttl: 5}
	result, err := service.Query(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Accounts) != 4 {
		t.Fatalf("accounts = %d, want 4: %#v", len(result.Accounts), result.Accounts)
	}
	if result.Accounts[0].Status != "available" {
		t.Fatalf("first account should succeed: %#v", result.Accounts[0])
	}
	for _, account := range result.Accounts[1:] {
		if account.Status != "error" || account.Error == "" {
			t.Fatalf("failed/unavailable account must remain visible: %#v", account)
		}
		if account.CredentialName == "" {
			t.Fatalf("credential name must be preserved: %#v", account)
		}
	}
}
