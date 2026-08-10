// Package balance 提供各供应商余额/订阅/用量查询能力。
// 核心思路：
//   - Provider 接口抽象不同供应商的余额查询（URL、认证、响应解析各不相同）
//   - BalanceResult 为规范化结果，统一存入 channel_balances 表
//   - 保留原始响应 raw_data，供应商接口变化时无需改表
package balance

import (
	"net/http"
	"strings"
	"time"

	"github.com/never/zero-api/internal/store"
)

// ResultType 余额数据类型
type ResultType string

const (
	TypeBalance     ResultType = "balance"     // 金额型余额（预付费/信用点）
	TypeSubscription ResultType = "subscription" // 订阅型
	TypeQuota       ResultType = "quota"       // token 配额型
	TypeUnknown     ResultType = "unknown"     // 未知/未查询
)

// BalanceResult 规范化余额查询结果（存入 channel_balances 表）
type BalanceResult struct {
	// 金额型
	Balance    float64 // 可用余额
	Currency   string  // 币种（USD/CNY）
	UsedAmount float64 // 已使用金额

	// 订阅型
	PlanType   string // 订阅计划名（go / zen / plus / claude-max）
	PlanStatus string // active / canceled / expired / trialing
	RenewsAt   string // 下次续费时间
	ExpiresAt  string // 到期时间

	// token 配额型
	TokenQuota     int64 // 配额总量
	TokenUsed      int64 // 已用
	TokenRemaining int64 // 剩余

	// 通用
	Provider string // 实际使用的适配器名
	Status   string // ok / warning / error / unsupported
	ErrorMsg string // 查询失败原因
	RawData  string // 供应商原始响应 JSON（保留完整信息）
}

// ToChannelBalance 转换为存储结构
func (r *BalanceResult) ToChannelBalance(channelID int64) *store.ChannelBalance {
	now := time.Now()
	return &store.ChannelBalance{
		ChannelID:      channelID,
		Balance:        r.Balance,
		Currency:       r.Currency,
		UsedAmount:     r.UsedAmount,
		PlanType:       r.PlanType,
		PlanStatus:     r.PlanStatus,
		RenewsAt:       r.RenewsAt,
		ExpiresAt:      r.ExpiresAt,
		TokenQuota:     r.TokenQuota,
		TokenUsed:      r.TokenUsed,
		TokenRemaining: r.TokenRemaining,
		Provider:       r.Provider,
		Status:         r.Status,
		ErrorMsg:       r.ErrorMsg,
		RawData:        r.RawData,
		LastCheckedAt:  now,
		UpdatedAt:      now,
	}
}

// Provider 余额查询适配器接口
type Provider interface {
	// Name 适配器名称（deepseek / moonshot / openrouter / glm / minimax / opencode / openai / none）
	Name() string

	// Enabled 是否可用
	Enabled() bool

	// BuildRequests 构造余额查询请求（可能多个：如 OpenAI 订阅+用量）
	BuildRequests(ch *store.Channel) ([]*http.Request, error)

	// ParseResponses 解析供应商响应为规范化结果
	// 多个请求时按顺序传入对应响应体
	ParseResponses(bodies [][]byte) (*BalanceResult, error)
}

// Registry 内置适配器注册表
type Registry struct {
	providers map[string]Provider
}

// NewRegistry 创建注册表并注册全部内置适配器
func NewRegistry() *Registry {
	r := &Registry{providers: make(map[string]Provider)}
	for _, p := range []Provider{
		&DeepSeekProvider{},
		&MoonshotProvider{},
		&OpenRouterProvider{},
		&GLMProvider{},
		&MiniMaxProvider{},
		&OpenCodeProvider{},
		&OpenAIProvider{},
		&NoneProvider{},
	} {
		r.providers[p.Name()] = p
	}
	return r
}

// Get 按名称获取适配器，不存在返回 nil
func (r *Registry) Get(name string) Provider {
	p, ok := r.providers[name]
	if !ok {
		return nil
	}
	return p
}

// List 列出所有可用适配器（供前端下拉）
func (r *Registry) List() []ProviderInfo {
	var out []ProviderInfo
	for _, p := range r.providers {
		if !p.Enabled() {
			continue
		}
		out = append(out, ProviderInfo{Name: p.Name()})
	}
	return out
}

// ProviderInfo 适配器信息（供前端展示）
type ProviderInfo struct {
	Name string `json:"name"`
}

// DetectProvider 根据渠道 base_url 域名启发式推断适配器名称
func DetectProvider(baseURL string) string {
	host := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://"))
	host = strings.TrimSuffix(host, "/")
	switch {
	case strings.Contains(host, "deepseek"):
		return "deepseek"
	case strings.Contains(host, "moonshot"):
		return "moonshot"
	case strings.Contains(host, "openrouter"):
		return "openrouter"
	case strings.Contains(host, "bigmodel") || strings.Contains(host, "zhipu"):
		return "glm"
	case strings.Contains(host, "minimax"):
		return "minimax"
	case strings.Contains(host, "opencode"):
		return "opencode"
	case strings.Contains(host, "openai"):
		return "openai"
	}
	return "none"
}
