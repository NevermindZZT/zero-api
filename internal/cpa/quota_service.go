package cpa

import (
	"context"
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
	return &QuotaService{client: client, providers: []QuotaProvider{CodexQuotaProvider{}}, ttl: 5 * time.Minute}
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
	result := &QuotaResponse{Provider: "codex", Accounts: []*QuotaSnapshot{}, QueriedAt: time.Now().UTC()}
	provider := CodexQuotaProvider{}
	for _, auth := range authFiles {
		if !provider.Match(auth) {
			continue
		}
		snapshot, queryErr := provider.Query(ctx, s.client, auth)
		if queryErr != nil {
			snapshot = &QuotaSnapshot{
				Provider: "codex", AuthIndex: auth.AuthIndex, AccountID: auth.AccountID,
				Email: auth.Email, PlanType: auth.PlanType, Status: "error",
				QueriedAt: time.Now().UTC(), Error: queryErr.Error(),
			}
		}
		result.Accounts = append(result.Accounts, snapshot)
	}

	s.mu.Lock()
	result.Cached = false
	s.cached = result
	s.cachedAt = time.Now()
	s.mu.Unlock()
	return result, nil
}
