package cpa

import (
	"context"
	"errors"
	"net/http"
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

func fallbackQuotaProvider(providers []QuotaProvider, auth AuthFile) QuotaProvider {
	for _, provider := range providers {
		if providerOwnsAuth(provider, auth) {
			return provider
		}
	}
	return nil
}

func nativeQuotaEndpointUnavailable(err error) bool {
	var apiErr *managementHTTPError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusMethodNotAllowed || apiErr.StatusCode == http.StatusNotImplemented
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
	for _, auth := range authFiles {
		legacyProvider := fallbackQuotaProvider(s.providers, auth)
		providerID := strings.TrimSpace(auth.Provider)
		if providerID == "" {
			continue
		}
		if reason := authUnavailableReason(auth); reason != "" {
			if legacyProvider != nil {
				result.Accounts = append(result.Accounts, errorQuotaSnapshot(legacyProvider.ID(), auth, reason))
			}
			continue
		}
		// Keep existing exclusions (for example Codex API keys) when a local legacy provider owns the credential.
		if legacyProvider != nil && !legacyProvider.Match(auth) {
			continue
		}

		quotaResult, queryErr := s.client.FetchCredentialQuota(ctx, auth)
		if queryErr == nil {
			result.Accounts = append(result.Accounts, &QuotaSnapshot{
				Provider: providerID, AuthIndex: auth.AuthIndex, CredentialName: auth.Name,
				AccountID: auth.AccountID, Email: auth.Email, PlanType: auth.PlanType,
				Status: "available", QuotaSource: "cpa", CPAResult: quotaResult, QueriedAt: time.Now().UTC(),
			})
			continue
		}

		if nativeQuotaEndpointUnavailable(queryErr) {
			// Older CPA versions may not expose quota/fetch. Keep the legacy provider
			// as a compatibility fallback, but do not parse provider payloads on the normal path.
			if legacyProvider == nil {
				continue
			}
			snapshot, legacyErr := legacyProvider.Query(ctx, s.client, auth)
			if legacyErr != nil {
				snapshot = errorQuotaSnapshot(legacyProvider.ID(), auth, legacyErr.Error())
			} else {
				snapshot.CredentialName = auth.Name
			}
			if snapshot.QuotaSource == "" {
				snapshot.QuotaSource = "legacy"
			}
			result.Accounts = append(result.Accounts, snapshot)
			continue
		}

		result.Accounts = append(result.Accounts, errorQuotaSnapshot(providerID, auth, queryErr.Error()))
	}

	s.mu.Lock()
	result.Cached = false
	s.cached = result
	s.cachedAt = time.Now()
	s.mu.Unlock()
	return result, nil
}
