package cpa

import (
	"context"
	"strings"
	"sync"
	"time"
)

// QuotaService 查询并缓存各 provider 额度。
type QuotaService struct {
	client    *ManagementClient
	providers []QuotaProvider
	ttl       time.Duration
	mu        sync.Mutex
	cached    *QuotaResponse
	cachedAt  time.Time
}

type QuotaResponse struct {
	Provider  string           `json:"provider"`
	Accounts  []*QuotaSnapshot `json:"accounts"`
	QueriedAt time.Time        `json:"queried_at"`
	Cached    bool             `json:"cached"`
	Message   string           `json:"message,omitempty"`
}

func NewQuotaService(client *ManagementClient) *QuotaService {
	return &QuotaService{client: client, providers: []QuotaProvider{CodexQuotaProvider{}, AntigravityQuotaProvider{}}, ttl: 5 * time.Minute}
}

func (s *QuotaService) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached = nil
	s.cachedAt = time.Time{}
}

func (s *QuotaService) UpdateEndpoint(host string, port int) {
	if s == nil || s.client == nil {
		return
	}
	s.client.UpdateEndpoint(host, port)
	s.Invalidate()
}

func providerOwnsAuth(provider QuotaProvider, auth AuthFile) bool {
	return strings.EqualFold(strings.TrimSpace(provider.ID()), strings.TrimSpace(auth.Provider))
}

func authUnavailableReason(auth AuthFile) string {
	if strings.TrimSpace(auth.AuthIndex) == "" {
		return "认证记录缺少 auth_index，无法查询额度"
	}
	if auth.Disabled {
		return "该订阅已禁用"
	}
	if auth.Unavailable {
		if auth.StatusMessage != "" {
			return "该订阅当前不可用：" + auth.StatusMessage
		}
		return "该订阅当前不可用"
	}
	switch strings.ToLower(strings.TrimSpace(auth.Status)) {
	case "disabled", "error", "revoked", "unavailable":
		if auth.StatusMessage != "" {
			return "认证状态异常：" + auth.StatusMessage
		}
		return "认证状态异常：" + auth.Status
	}
	return ""
}

func errorQuotaSnapshot(provider string, auth AuthFile, message string) *QuotaSnapshot {
	return &QuotaSnapshot{
		Provider: provider, AuthIndex: auth.AuthIndex, CredentialName: auth.Name,
		AccountID: auth.AccountID, Email: auth.Email, PlanType: auth.PlanType,
		Status: "error", QueriedAt: time.Now().UTC(), Error: message,
	}
}

func (s *QuotaService) Query(ctx context.Context, refresh bool) (*QuotaResponse, error) {
	s.mu.Lock()
	if !refresh && s.cached != nil && time.Since(s.cachedAt) < s.ttl {
		copy := *s.cached
		copy.Cached = true
		s.mu.Unlock()
		return &copy, nil
	}
	s.mu.Unlock()

	authFiles, err := s.client.GetAuthFiles(ctx)
	if err != nil {
		return nil, err
	}
	result := &QuotaResponse{Provider: "all", Accounts: []*QuotaSnapshot{}, QueriedAt: time.Now().UTC()}
	for _, provider := range s.providers {
		for _, auth := range authFiles {
			if !providerOwnsAuth(provider, auth) {
				continue
			}
			if reason := authUnavailableReason(auth); reason != "" {
				result.Accounts = append(result.Accounts, errorQuotaSnapshot(provider.ID(), auth, reason))
				continue
			}
			if !provider.Match(auth) {
				continue
			}
			snapshot, queryErr := provider.Query(ctx, s.client, auth)
			if queryErr != nil {
				snapshot = errorQuotaSnapshot(provider.ID(), auth, queryErr.Error())
			} else {
				snapshot.CredentialName = auth.Name
			}
			result.Accounts = append(result.Accounts, snapshot)
		}
	}

	s.mu.Lock()
	result.Cached = false
	s.cached = result
	s.cachedAt = time.Now()
	s.mu.Unlock()
	return result, nil
}
