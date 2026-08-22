package cpa

import (
	"context"
	"testing"
)

type fakeQuotaProvider struct{}

func (fakeQuotaProvider) ID() string          { return "fake" }
func (fakeQuotaProvider) Match(AuthFile) bool { return true }
func (fakeQuotaProvider) Query(context.Context, *ManagementClient, AuthFile) (*QuotaSnapshot, error) {
	return &QuotaSnapshot{Provider: "fake"}, nil
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
