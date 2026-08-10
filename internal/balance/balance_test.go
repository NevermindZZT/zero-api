package balance

import (
	"testing"
)

// ===== 各供应商响应解析 =====

func TestDeepSeekParse(t *testing.T) {
	p := &DeepSeekProvider{}
	body := []byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"9.99","granted_balance":"0.00","topped_up_balance":"9.99"}]}`)
	r, err := p.ParseResponses([][]byte{body})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.Balance != 9.99 {
		t.Errorf("余额应为 9.99，got %v", r.Balance)
	}
	if r.Currency != "CNY" {
		t.Errorf("币种应为 CNY，got %s", r.Currency)
	}
	if r.Status != "ok" {
		t.Errorf("状态应为 ok，got %s", r.Status)
	}
}

func TestMoonshotParse(t *testing.T) {
	p := &MoonshotProvider{}
	// 单位分：17015643.50 分 = 170156.435 元
	body := []byte(`{"data":{"available_balance":17015643.50,"voucher_balance":0,"cash_balance":17015643.50},"code":0}`)
	r, err := p.ParseResponses([][]byte{body})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.Balance != 170156.435 {
		t.Errorf("余额应为 170156.435（分转元），got %v", r.Balance)
	}
	if r.Currency != "CNY" {
		t.Errorf("币种应为 CNY，got %s", r.Currency)
	}
}

func TestOpenRouterParse(t *testing.T) {
	p := &OpenRouterProvider{}
	body := []byte(`{"data":{"total_credits":25.412125,"total_usage":0.541572,"has_payment_method":true},"error":null}`)
	r, err := p.ParseResponses([][]byte{body})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.Balance < 24.87 || r.Balance > 24.88 {
		t.Errorf("余额应为约 24.87，got %v", r.Balance)
	}
	if r.UsedAmount != 0.541572 {
		t.Errorf("已用应为 0.541572，got %v", r.UsedAmount)
	}
	if r.PlanType != "credits" {
		t.Errorf("计划应为 credits，got %s", r.PlanType)
	}
}

func TestGLMParse(t *testing.T) {
	p := &GLMProvider{}
	body := []byte(`{"balance":{"total":202.3295,"used":34.6815,"remaining":167.648},"code":200}`)
	r, err := p.ParseResponses([][]byte{body})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.Balance != 167.648 {
		t.Errorf("余额应为 167.648，got %v", r.Balance)
	}
	if r.UsedAmount != 34.6815 {
		t.Errorf("已用应为 34.6815，got %v", r.UsedAmount)
	}
}

func TestMiniMaxParse(t *testing.T) {
	p := &MiniMaxProvider{}
	body := []byte(`{"total_balance":"123.45","used_balance":"23.45","remaining_balance":"100.00","currency":"CNY"}`)
	r, err := p.ParseResponses([][]byte{body})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.Balance != 100.00 {
		t.Errorf("余额应为 100.00，got %v", r.Balance)
	}
	if r.Currency != "CNY" {
		t.Errorf("币种应为 CNY，got %s", r.Currency)
	}
}

// ===== OpenAI 格式（OpenAI 官方）=====

func TestOpenAILikeParse(t *testing.T) {
	p := &openAILikeProvider{name: "opencode"}
	subBody := []byte(`{"object":"billing_subscription","has_payment_method":true,"cancel_at_period_end":false,"plan":{"id":"go","title":"Go"},"current_period_end":1786060800,"hard_limit_usd":10}`)
	usageBody := []byte(`{"object":"list","total_usage":3.456}`)
	r, err := p.ParseResponses([][]byte{subBody, usageBody})
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.PlanType != "Go" {
		t.Errorf("计划应为 Go，got %s", r.PlanType)
	}
	if r.PlanStatus != "active" {
		t.Errorf("状态应为 active，got %s", r.PlanStatus)
	}
	if r.UsedAmount != 3.456 {
		t.Errorf("已用应为 3.456，got %v", r.UsedAmount)
	}
	if r.RenewsAt == "" {
		t.Error("续费时间不应为空")
	}
	if r.Provider != "opencode" {
		t.Errorf("提供方应为 opencode，got %s", r.Provider)
	}
}

// ===== OpenCode（无公开 API，返回 unsupported）=====

func TestOpenCodeParse(t *testing.T) {
	p := &OpenCodeProvider{}
	r, err := p.ParseResponses(nil)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.Status != "unsupported" {
		t.Errorf("状态应为 unsupported，got %s", r.Status)
	}
	if r.Provider != "opencode" {
		t.Errorf("提供方应为 opencode，got %s", r.Provider)
	}
	if r.ErrorMsg == "" {
		t.Error("应有说明信息")
	}
}

// ===== 域名推断 =====

func TestDetectProvider(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://api.deepseek.com", "deepseek"},
		{"https://api.moonshot.cn", "moonshot"},
		{"https://openrouter.ai/api/v1", "openrouter"},
		{"https://open.bigmodel.cn", "glm"},
		{"https://api.minimax.chat", "minimax"},
		{"https://opencode.ai/zen", "opencode"},
		{"https://api.openai.com", "openai"},
		{"https://api.anthropic.com", "none"},
		{"https://generativelanguage.googleapis.com", "none"},
	}
	for _, c := range cases {
		got := DetectProvider(c.url)
		if got != c.want {
			t.Errorf("DetectProvider(%s) 应为 %s，got %s", c.url, c.want, got)
		}
	}
}

// ===== Registry =====

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	// 内置适配器都在
	for _, name := range []string{"deepseek", "moonshot", "openrouter", "glm", "minimax", "opencode", "openai", "none"} {
		if r.Get(name) == nil {
			t.Errorf("适配器 %s 未注册", name)
		}
	}
	// 不存在的返回 nil
	if r.Get("nonexistent") != nil {
		t.Error("不存在的适配器应返回 nil")
	}
}
