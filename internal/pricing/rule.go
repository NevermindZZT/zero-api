// Package pricing 提供灵活定价规则引擎
// 支持分时间段定价和分请求 Token 大小阶梯定价
package pricing

import (
	"encoding/json"
	"fmt"
	"time"
)

// RuleType 定价规则类型
type RuleType string

const (
	RuleTypeTimeRange RuleType = "time_range" // 时间段定价
	RuleTypeTokenTier RuleType = "token_tier" // Token 大小阶梯
)

// PricingSet 一组定价
type PricingSet struct {
	Input      float64 `json:"pricing_input"`       // $/1M tokens
	Output     float64 `json:"pricing_output"`      // $/1M tokens
	CacheRead  float64 `json:"pricing_cache_read"`  // $/1M tokens
	CacheWrite float64 `json:"pricing_cache_write"` // $/1M tokens
}

// PricingRule 单条定价规则
type PricingRule struct {
	ID      string   `json:"id"`
	Type    RuleType `json:"type"`
	Enabled bool     `json:"enabled"`
	Name    string   `json:"name"`

	// 时间段条件（type=time_range）
	Days      []string `json:"days,omitempty"`      // ["mon","tue",...,"sun"]，空=每天
	StartTime string   `json:"start_time,omitempty"` // "00:00"
	EndTime   string   `json:"end_time,omitempty"`   // "08:00"，支持跨天(22:00-06:00)

	// Token 阶梯条件（type=token_tier）
	PromptMaxTokens  int `json:"prompt_max_tokens,omitempty"`  // prompt_tokens <= 此值
	ContextMaxTokens int `json:"context_max_tokens,omitempty"` // total_tokens <= 此值

	// 该规则适用的定价
	PricingInput      float64 `json:"pricing_input"`
	PricingOutput     float64 `json:"pricing_output"`
	PricingCacheRead  float64 `json:"pricing_cache_read"`
	PricingCacheWrite float64 `json:"pricing_cache_write"`
}

// PricingRules 定价规则列表（有序，优先级按索引降序）
type PricingRules []PricingRule

// ParsePricingRules 解析 JSON 字符串为定价规则列表
func ParsePricingRules(s string) (PricingRules, error) {
	if s == "" {
		return nil, nil
	}
	var rules PricingRules
	if err := json.Unmarshal([]byte(s), &rules); err != nil {
		return nil, fmt.Errorf("解析定价规则失败: %w", err)
	}
	return rules, nil
}

// MustParsePricingRules 解析 JSON 字符串，失败时返回空列表
func MustParsePricingRules(s string) PricingRules {
	rules, _ := ParsePricingRules(s)
	return rules
}

// String 序列化为 JSON 字符串
func (r PricingRules) String() string {
	if len(r) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(r)
	return string(b)
}

// Validate 校验定价规则列表
func (r PricingRules) Validate() error {
	seen := make(map[string]bool)
	for i, rule := range r {
		if rule.ID == "" {
			return fmt.Errorf("规则 #%d: ID 不能为空", i+1)
		}
		if seen[rule.ID] {
			return fmt.Errorf("规则 ID %q 重复", rule.ID)
		}
		seen[rule.ID] = true

		switch rule.Type {
		case RuleTypeTimeRange:
			if rule.StartTime == "" || rule.EndTime == "" {
				return fmt.Errorf("规则 %q: 时间段规则需要 start_time 和 end_time", rule.ID)
			}
			if _, err := time.Parse("15:04", rule.StartTime); err != nil {
				return fmt.Errorf("规则 %q: start_time 格式无效 %q: %w", rule.ID, rule.StartTime, err)
			}
			if _, err := time.Parse("15:04", rule.EndTime); err != nil {
				return fmt.Errorf("规则 %q: end_time 格式无效 %q: %w", rule.ID, rule.EndTime, err)
			}
			for _, d := range rule.Days {
				switch d {
				case "mon", "tue", "wed", "thu", "fri", "sat", "sun":
				default:
					return fmt.Errorf("规则 %q: 无效的星期值 %q", rule.ID, d)
				}
			}
		case RuleTypeTokenTier:
			if rule.PromptMaxTokens <= 0 && rule.ContextMaxTokens <= 0 {
				return fmt.Errorf("规则 %q: Token 阶梯规则需要至少设置 prompt_max_tokens 或 context_max_tokens", rule.ID)
			}
		default:
			return fmt.Errorf("规则 %q: 未知规则类型 %q", rule.ID, rule.Type)
		}

		// 检查是否至少有一个定价字段 > 0（允许全部为零，但给个 warning 语义）
		pz := rule.PricingInput <= 0 && rule.PricingOutput <= 0 &&
			rule.PricingCacheRead <= 0 && rule.PricingCacheWrite <= 0
		if !rule.Enabled && pz {
			return fmt.Errorf("规则 %q: 启用的规则需要至少设置一个定价字段", rule.ID)
		}
	}
	return nil
}
