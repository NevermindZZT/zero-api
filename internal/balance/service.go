package balance

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/never/zero-api/internal/store"
)

// Service 余额查询编排服务
type Service struct {
	registry       *Registry
	channelRepo    *store.ChannelRepo
	balanceRepo    *store.ChannelBalanceRepo
	httpClient     *http.Client
	requestTimeout time.Duration
}

// NewService 创建余额查询服务
func NewService(channelRepo *store.ChannelRepo, balanceRepo *store.ChannelBalanceRepo) *Service {
	return &Service{
		registry:       NewRegistry(),
		channelRepo:    channelRepo,
		balanceRepo:    balanceRepo,
		httpClient:     &http.Client{Timeout: 15 * time.Second},
		requestTimeout: 15 * time.Second,
	}
}

// Providers 列出可用适配器（前端下拉）
func (s *Service) Providers() []ProviderInfo {
	return s.registry.List()
}

// GetByChannel 获取渠道已缓存的余额
func (s *Service) GetByChannel(channelID int64) (*store.ChannelBalance, error) {
	return s.balanceRepo.GetByChannel(channelID)
}

// List 列出所有渠道余额
func (s *Service) List() ([]store.ChannelBalance, error) {
	return s.balanceRepo.ListAll()
}

// Refresh 刷新单个渠道余额，返回更新后的记录
// 不限制渠道启用状态：禁用渠道也允许查询余额（余额查询是旁路操作，不影响转发）
func (s *Service) Refresh(channelID int64) (*store.ChannelBalance, error) {
	ch, err := s.channelRepo.GetByID(channelID)
	if err != nil {
		return nil, fmt.Errorf("获取渠道失败: %w", err)
	}

	result := s.query(ch)

	// 规范化默认值
	if result.Provider == "" {
		result.Provider = "none"
	}
	if result.Status == "" {
		result.Status = "ok"
	}

	cb := result.ToChannelBalance(channelID)
	if err := s.balanceRepo.Upsert(cb); err != nil {
		return nil, fmt.Errorf("保存余额失败: %w", err)
	}
	return s.balanceRepo.GetByChannel(channelID)
}

// SetManualBalance 手动设置渠道余额（用于无公开余额 API 的供应商，如 OpenCode）
// 已存在的记录会被更新为 manual 状态，保留其余已查询字段
func (s *Service) SetManualBalance(channelID int64, balance float64, currency string) (*store.ChannelBalance, error) {
	existing, err := s.balanceRepo.GetByChannel(channelID)
	if err != nil {
		return nil, fmt.Errorf("获取渠道余额失败: %w", err)
	}

	now := time.Now()
	cb := &store.ChannelBalance{
		ChannelID:      channelID,
		Balance:        balance,
		Currency:       currency,
		Provider:       "manual",
		Status:         "manual",
		RawData:        "{}",
		LastCheckedAt:  now,
		UpdatedAt:      now,
	}
	if existing != nil {
		// 保留已查询到的其他维度信息
		cb.UsedAmount = existing.UsedAmount
		cb.PlanType = existing.PlanType
		cb.PlanStatus = existing.PlanStatus
		cb.RenewsAt = existing.RenewsAt
		cb.ExpiresAt = existing.ExpiresAt
		cb.TokenQuota = existing.TokenQuota
		cb.TokenUsed = existing.TokenUsed
		cb.TokenRemaining = existing.TokenRemaining
		cb.RawData = existing.RawData
	}
	if err := s.balanceRepo.Upsert(cb); err != nil {
		return nil, fmt.Errorf("保存余额失败: %w", err)
	}
	return s.balanceRepo.GetByChannel(channelID)
}

// RefreshAll 批量刷新所有启用渠道（串行），返回结果列表
func (s *Service) RefreshAll() []store.ChannelBalance {
	channels, err := s.channelRepo.List()
	if err != nil {
		log.Printf("[余额] 列出渠道失败: %v", err)
		return nil
	}
	var results []store.ChannelBalance
	for _, ch := range channels {
		if ch.Status != "active" {
			continue
		}
		cb, err := s.Refresh(ch.ID)
		if err != nil {
			log.Printf("[余额] 渠道 %s 刷新失败: %v", ch.Name, err)
			continue
		}
		if cb != nil {
			results = append(results, *cb)
		}
	}
	return results
}

// query 实际查询渠道余额
func (s *Service) query(ch *store.Channel) *BalanceResult {
	provider := autoSelect(s.registry, ch)

	reqs, err := provider.BuildRequests(ch)
	if err != nil {
		return &BalanceResult{
			Provider: provider.Name(),
			Status:   "error",
			ErrorMsg: err.Error(),
		}
	}

	var bodies [][]byte
	for _, req := range reqs {
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return &BalanceResult{
				Provider: provider.Name(),
				Status:   "error",
				ErrorMsg: fmt.Sprintf("请求失败: %v", err),
			}
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			msg := string(body)
			if len(msg) > 200 {
				msg = msg[:200]
			}
			return &BalanceResult{
				Provider: provider.Name(),
				Status:   "error",
				ErrorMsg: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, msg),
			}
		}
		bodies = append(bodies, body)
	}

	result, err := provider.ParseResponses(bodies)
	if err != nil {
		return &BalanceResult{
			Provider: provider.Name(),
			Status:   "error",
			ErrorMsg: err.Error(),
		}
	}
	return result
}

// autoSelect 根据渠道配置选择适配器
// balance_api: auto(按域名推断) / 具体适配器名 / none
func autoSelect(registry *Registry, ch *store.Channel) Provider {
	api := ch.BalanceAPI
	if api == "" || api == "auto" {
		name := DetectProvider(ch.BaseURL)
		p := registry.Get(name)
		if p != nil {
			return p
		}
		return registry.Get("none")
	}
	p := registry.Get(api)
	if p != nil {
		return p
	}
	return registry.Get("none")
}
