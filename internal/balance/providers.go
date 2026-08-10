package balance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/never/zero-api/internal/store"
)

// ===== DeepSeek =====
// GET {base}/user/balance
// 响应: {"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"9.99","granted_balance":"0.00","topped_up_balance":"9.99"}]}

type DeepSeekProvider struct{}

func (p *DeepSeekProvider) Name() string    { return "deepseek" }
func (p *DeepSeekProvider) Enabled() bool   { return true }

func (p *DeepSeekProvider) BuildRequests(ch *store.Channel) ([]*http.Request, error) {
	url := strings.TrimRight(ch.BaseURL, "/") + "/user/balance"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if ch.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+ch.APIKey)
	}
	return []*http.Request{req}, nil
}

func (p *DeepSeekProvider) ParseResponses(bodies [][]byte) (*BalanceResult, error) {
	if len(bodies) == 0 {
		return nil, fmt.Errorf("无响应")
	}
	var resp struct {
		BalanceInfos []struct {
			Currency       string `json:"currency"`
			TotalBalance   string `json:"total_balance"`
			GrantedBalance string `json:"granted_balance"`
			ToppedUpBalance string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(bodies[0], &resp); err != nil {
		return nil, fmt.Errorf("解析 DeepSeek 余额响应失败: %w", err)
	}
	if len(resp.BalanceInfos) == 0 {
		return nil, fmt.Errorf("响应中无 balance_infos")
	}
	info := resp.BalanceInfos[0]
	balance := parseFloat(info.TotalBalance)
	return &BalanceResult{
		Balance:  balance,
		Currency: info.Currency,
		UsedAmount: 0,
		Provider: "deepseek",
		Status:   "ok",
		RawData:  string(bodies[0]),
	}, nil
}

// ===== Moonshot / Kimi =====
// GET {base}/v1/users/me/balance
// 响应: {"data":{"available_balance":17015643.50,"voucher_balance":0,"cash_balance":17015643.50},"code":0}
// 金额单位：分（cents）

type MoonshotProvider struct{}

func (p *MoonshotProvider) Name() string  { return "moonshot" }
func (p *MoonshotProvider) Enabled() bool { return true }

func (p *MoonshotProvider) BuildRequests(ch *store.Channel) ([]*http.Request, error) {
	url := strings.TrimRight(ch.BaseURL, "/") + "/v1/users/me/balance"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if ch.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+ch.APIKey)
	}
	return []*http.Request{req}, nil
}

func (p *MoonshotProvider) ParseResponses(bodies [][]byte) (*BalanceResult, error) {
	if len(bodies) == 0 {
		return nil, fmt.Errorf("无响应")
	}
	var resp struct {
		Data struct {
			AvailableBalance float64 `json:"available_balance"`
			VoucherBalance   float64 `json:"voucher_balance"`
			CashBalance      float64 `json:"cash_balance"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bodies[0], &resp); err != nil {
		return nil, fmt.Errorf("解析 Moonshot 余额响应失败: %w", err)
	}
	// 单位分 → 元
	balance := resp.Data.AvailableBalance / 100
	return &BalanceResult{
		Balance:  balance,
		Currency: "CNY",
		UsedAmount: 0,
		Provider: "moonshot",
		Status:   "ok",
		RawData:  string(bodies[0]),
	}, nil
}

// ===== OpenRouter =====
// GET {base}/api/v1/credits
// 响应: {"data":{"total_credits":25.412125,"total_usage":0.541572,"has_payment_method":true},"error":null}
// 金额单位：美元

type OpenRouterProvider struct{}

func (p *OpenRouterProvider) Name() string  { return "openrouter" }
func (p *OpenRouterProvider) Enabled() bool { return true }

func (p *OpenRouterProvider) BuildRequests(ch *store.Channel) ([]*http.Request, error) {
	url := strings.TrimRight(ch.BaseURL, "/") + "/api/v1/credits"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if ch.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+ch.APIKey)
	}
	return []*http.Request{req}, nil
}

func (p *OpenRouterProvider) ParseResponses(bodies [][]byte) (*BalanceResult, error) {
	if len(bodies) == 0 {
		return nil, fmt.Errorf("无响应")
	}
	var resp struct {
		Data struct {
			TotalCredits     float64 `json:"total_credits"`
			TotalUsage       float64 `json:"total_usage"`
			HasPaymentMethod bool    `json:"has_payment_method"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bodies[0], &resp); err != nil {
		return nil, fmt.Errorf("解析 OpenRouter 余额响应失败: %w", err)
	}
	return &BalanceResult{
		Balance:    resp.Data.TotalCredits - resp.Data.TotalUsage,
		Currency:   "USD",
		UsedAmount: resp.Data.TotalUsage,
		PlanType:   "credits",
		Provider:   "openrouter",
		Status:     "ok",
		RawData:    string(bodies[0]),
	}, nil
}

// ===== 智谱 GLM =====
// GET {base}/api/paas/v4/billing/balance
// 响应: {"balance":{"total":202.3295,"used":34.6815,"remaining":167.648},"code":200}
// 金额单位：元（CNY）

type GLMProvider struct{}

func (p *GLMProvider) Name() string  { return "glm" }
func (p *GLMProvider) Enabled() bool { return true }

func (p *GLMProvider) BuildRequests(ch *store.Channel) ([]*http.Request, error) {
	url := strings.TrimRight(ch.BaseURL, "/") + "/api/paas/v4/billing/balance"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if ch.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+ch.APIKey)
	}
	return []*http.Request{req}, nil
}

func (p *GLMProvider) ParseResponses(bodies [][]byte) (*BalanceResult, error) {
	if len(bodies) == 0 {
		return nil, fmt.Errorf("无响应")
	}
	var resp struct {
		Balance struct {
			Total     float64 `json:"total"`
			Used      float64 `json:"used"`
			Remaining float64 `json:"remaining"`
		} `json:"balance"`
	}
	if err := json.Unmarshal(bodies[0], &resp); err != nil {
		return nil, fmt.Errorf("解析 GLM 余额响应失败: %w", err)
	}
	return &BalanceResult{
		Balance:    resp.Balance.Remaining,
		Currency:   "CNY",
		UsedAmount: resp.Balance.Used,
		Provider:   "glm",
		Status:     "ok",
		RawData:    string(bodies[0]),
	}, nil
}

// ===== MiniMax =====
// GET {base}/v1/query/balance
// 响应: {"total_balance":"123.45","used_balance":"23.45","remaining_balance":"100.00","currency":"CNY"}

type MiniMaxProvider struct{}

func (p *MiniMaxProvider) Name() string  { return "minimax" }
func (p *MiniMaxProvider) Enabled() bool { return true }

func (p *MiniMaxProvider) BuildRequests(ch *store.Channel) ([]*http.Request, error) {
	url := strings.TrimRight(ch.BaseURL, "/") + "/v1/query/balance"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if ch.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+ch.APIKey)
	}
	return []*http.Request{req}, nil
}

func (p *MiniMaxProvider) ParseResponses(bodies [][]byte) (*BalanceResult, error) {
	if len(bodies) == 0 {
		return nil, fmt.Errorf("无响应")
	}
	var resp struct {
		TotalBalance     string `json:"total_balance"`
		UsedBalance      string `json:"used_balance"`
		RemainingBalance string `json:"remaining_balance"`
		Currency         string `json:"currency"`
	}
	if err := json.Unmarshal(bodies[0], &resp); err != nil {
		return nil, fmt.Errorf("解析 MiniMax 余额响应失败: %w", err)
	}
	return &BalanceResult{
		Balance:    parseFloat(resp.RemainingBalance),
		Currency:   resp.Currency,
		UsedAmount: parseFloat(resp.UsedBalance),
		Provider:   "minimax",
		Status:     "ok",
		RawData:    string(bodies[0]),
	}, nil
}

// parseFloat 解析字符串为 float64（容错：空串、非法返回 0）
func parseFloat(s string) float64 {
	var f float64
	_, _ = fmt.Sscanf(s, "%f", &f)
	return f
}
