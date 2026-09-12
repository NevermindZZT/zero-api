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

func TestQuotaServiceKeepsFailedAndUnavailableAccountsVisible(t *testing.T) {
	authFiles := []map[string]any{
		{"name": "ok.json", "provider": "fake", "auth_index": "ok", "email": "ok@example.com"},
		{"name": "fail.json", "provider": "fake", "auth_index": "fail", "email": "fail@example.com"},
		{"name": "disabled.json", "provider": "fake", "auth_index": "disabled", "email": "disabled@example.com", "disabled": true},
		{"name": "missing-index.json", "provider": "fake", "email": "missing@example.com"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/auth-files" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": authFiles})
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
