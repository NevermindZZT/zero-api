package balance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/never/zero-api/internal/store"
)

// ===== OpenAI 官方 Billing（OpenCode Zen 也兼容此格式）=====
// GET {base}/v1/dashboard/billing/subscription
//   响应: {"object":"billing_subscription","has_payment_method":true,"cancel_at_period_end":false,
//          "plan":{"id":"zen","title":"Zen"}, "current_period_start":..., "current_period_end":...,
//          "hard_limit_usd":20, "system_hard_limit_usd":100}
// GET {base}/v1/dashboard/billing/usage?start_date=YYYY-MM-DD&end_date=YYYY-MM-DD
//   响应: {"object":"list","daily_costs":[...],"total_usage":12.345}

// openAILikeProvider OpenAI Billing 格式适配器基类（OpenAI 官方 / OpenCode Zen 共用）
type openAILikeProvider struct {
	name string
}

func (p *openAILikeProvider) Name() string  { return p.name }
func (p *openAILikeProvider) Enabled() bool { return true }

func (p *openAILikeProvider) BuildRequests(ch *store.Channel) ([]*http.Request, error) {
	base := strings.TrimRight(ch.BaseURL, "/")

	// 订阅信息
	subReq, err := http.NewRequest("GET", base+"/v1/dashboard/billing/subscription", nil)
	if err != nil {
		return nil, err
	}

	// 当月用量（start=月初, end=今天）
	now := time.Now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	usageURL := fmt.Sprintf("%s/v1/dashboard/billing/usage?start_date=%s&end_date=%s",
		base, startOfMonth.Format("2006-01-02"), now.Format("2006-01-02"))
	usageReq, err := http.NewRequest("GET", usageURL, nil)
	if err != nil {
		return nil, err
	}

	if ch.APIKey != "" {
		auth := "Bearer " + ch.APIKey
		subReq.Header.Set("Authorization", auth)
		usageReq.Header.Set("Authorization", auth)
	}
	return []*http.Request{subReq, usageReq}, nil
}

// subscriptionResp 订阅响应（宽容解析：字段可能缺失）
type subscriptionResp struct {
	Object           string  `json:"object"`
	HasPaymentMethod bool    `json:"has_payment_method"`
	CancelAtPeriodEnd bool   `json:"cancel_at_period_end"`
	Plan             struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"plan"`
	CurrentPeriodStart int64 `json:"current_period_start"`
	CurrentPeriodEnd   int64 `json:"current_period_end"`
	HardLimitUSD       float64 `json:"hard_limit_usd"`
	SystemHardLimitUSD float64 `json:"system_hard_limit_usd"`
}

// usageResp 用量响应
type usageResp struct {
	TotalUsage float64 `json:"total_usage"`
}

func (p *openAILikeProvider) ParseResponses(bodies [][]byte) (*BalanceResult, error) {
	if len(bodies) < 1 {
		return nil, fmt.Errorf("无响应")
	}
	result := &BalanceResult{
		Provider: p.name,
		Status:   "ok",
	}

	// 解析订阅
	var sub subscriptionResp
	if err := json.Unmarshal(bodies[0], &sub); err == nil {
		result.PlanType = sub.Plan.ID
		if sub.Plan.Title != "" {
			result.PlanType = sub.Plan.Title
		}
		if sub.CancelAtPeriodEnd {
			result.PlanStatus = "canceled"
		} else if sub.HasPaymentMethod {
			result.PlanStatus = "active"
		} else {
			result.PlanStatus = "trialing"
		}
		if sub.CurrentPeriodEnd > 0 {
			result.RenewsAt = time.Unix(sub.CurrentPeriodEnd, 0).Format("2006-01-02")
		}
		if sub.HardLimitUSD > 0 {
			result.TokenQuota = int64(sub.HardLimitUSD * 100) // 限额（美分）
		}
	}

	// 解析用量（第二个响应）
	if len(bodies) > 1 {
		var usage usageResp
		if err := json.Unmarshal(bodies[1], &usage); err == nil {
			result.UsedAmount = usage.TotalUsage
			result.Balance = usage.TotalUsage
			result.Currency = "USD"
		}
	}

	// 原始数据合并
	var raws []string
	for _, b := range bodies {
		raws = append(raws, string(b))
	}
	result.RawData = strings.Join(raws, "\n---\n")
	return result, nil
}

// ===== OpenAI 官方 =====
type OpenAIProvider struct{}

func (p *OpenAIProvider) Name() string  { return "openai" }
func (p *OpenAIProvider) Enabled() bool { return true }

func (p *OpenAIProvider) BuildRequests(ch *store.Channel) ([]*http.Request, error) {
	return (&openAILikeProvider{name: "openai"}).BuildRequests(ch)
}

func (p *OpenAIProvider) ParseResponses(bodies [][]byte) (*BalanceResult, error) {
	return (&openAILikeProvider{name: "openai"}).ParseResponses(bodies)
}

// ===== OpenCode Zen =====
// OpenCode Zen / Go 没有公开的余额查询 API（已实测 /v1/dashboard/billing/subscription、
// /v1/usage、/v1/credits 等均 404）。余额仅在控制台 Web 界面（需登录 session）可见。
// 适配器返回 unsupported 状态，用户可通过前端“手动维护余额”功能记录余额。
type OpenCodeProvider struct{}

func (p *OpenCodeProvider) Name() string  { return "opencode" }
func (p *OpenCodeProvider) Enabled() bool { return true }

func (p *OpenCodeProvider) BuildRequests(ch *store.Channel) ([]*http.Request, error) {
	// 无查询端点，返回空请求（Service.query 会走 ParseResponses 的空 bodies 分支）
	return nil, nil
}

func (p *OpenCodeProvider) ParseResponses(bodies [][]byte) (*BalanceResult, error) {
	return &BalanceResult{
		Provider: "opencode",
		Status:   "unsupported",
		ErrorMsg: "OpenCode 不提供公开的余额查询接口，可在详情中手动维护余额",
		RawData:  "",
	}, nil
}

// ===== None（不支持查询）=====
type NoneProvider struct{}

func (p *NoneProvider) Name() string  { return "none" }
func (p *NoneProvider) Enabled() bool { return true }

func (p *NoneProvider) BuildRequests(ch *store.Channel) ([]*http.Request, error) {
	return nil, fmt.Errorf("该渠道未配置余额查询")
}

func (p *NoneProvider) ParseResponses(bodies [][]byte) (*BalanceResult, error) {
	return nil, fmt.Errorf("该渠道未配置余额查询")
}
