package pricing

import (
	"time"
)

// dayNames 星期缩写映射
var dayNames = map[time.Weekday]string{
	time.Monday:    "mon",
	time.Tuesday:   "tue",
	time.Wednesday: "wed",
	time.Thursday:  "thu",
	time.Friday:    "fri",
	time.Saturday:  "sat",
	time.Sunday:    "sun",
}

// weekDaySet 快速星期匹配集合
func weekDaySet(days []string) map[string]bool {
	s := make(map[string]bool, len(days))
	for _, d := range days {
		s[d] = true
	}
	return s
}

// timeWithinRange 判断时间是否在区间内，支持跨天
// 例如 22:00-06:00 表示前一晚 22 点到次日 6 点
// 00:00-00:00 表示全天
func timeWithinRange(t time.Time, startStr, endStr string) bool {
	start, _ := time.Parse("15:04", startStr)
	end, _ := time.Parse("15:04", endStr)

	// 00:00-00:00 = 全天
	if start.Hour() == 0 && start.Minute() == 0 &&
		end.Hour() == 0 && end.Minute() == 0 {
		return true
	}

	// 用当天的年月日构造比较时间
	now := time.Date(0, 1, 1, t.Hour(), t.Minute(), t.Second(), 0, t.Location())

	startTime := time.Date(0, 1, 1, start.Hour(), start.Minute(), 0, 0, t.Location())
	endTime := time.Date(0, 1, 1, end.Hour(), end.Minute(), 0, 0, t.Location())

	if startTime.Before(endTime) {
		// 同天区间：00:00-08:00
		return (now.Equal(startTime) || now.After(startTime)) && (now.Equal(endTime) || now.Before(endTime))
	}
	// 跨天区间：22:00-06:00，判断逻辑为 now >= start || now <= end
	return (now.Equal(startTime) || now.After(startTime)) || (now.Equal(endTime) || now.Before(endTime))
}

// dayName 获取星期的三字母缩写
func dayName(t time.Time) string {
	return dayNames[t.Weekday()]
}

// ResolvePricing 根据请求上下文解析实际使用的定价
// 参数：
//   - rules: 模型的定价规则列表
//   - flat: 模型的基础定价（无规则匹配时的 fallback）
//   - now: 当前时间
//   - promptTokens: 请求的 prompt token 数
//   - totalTokens: 请求的总 token 数
//
// 返回：
//   - matchedRuleID: 匹配到的规则 ID（空=无匹配）
//   - resolved: 最终定价
func ResolvePricing(rules PricingRules, flat PricingSet, now time.Time, promptTokens, totalTokens int) (matchedRuleID string, resolved PricingSet) {
	// 无规则时直接返回 flat
	if len(rules) == 0 {
		return "", flat
	}

	// 按规则列表顺序遍历，首条匹配即返回（first-match-wins）
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}

		if !matchRule(rule, now, promptTokens, totalTokens) {
			continue
		}

		// 匹配成功，使用规则中的定价
		matchedRuleID = rule.ID
		resolved = PricingSet{
			Input:      rule.PricingInput,
			Output:     rule.PricingOutput,
			CacheRead:  rule.PricingCacheRead,
			CacheWrite: rule.PricingCacheWrite,
		}
		return matchedRuleID, resolved
	}

	// 无规则匹配，返回 flat
	return "", flat
}

// matchRule 判断单条规则是否匹配当前请求上下文
func matchRule(rule PricingRule, now time.Time, promptTokens, totalTokens int) bool {
	switch rule.Type {
	case RuleTypeTimeRange:
		return matchTimeRange(rule, now)
	case RuleTypeTokenTier:
		return matchTokenTier(rule, promptTokens, totalTokens)
	default:
		return false
	}
}

// matchTimeRange 匹配时间段规则
func matchTimeRange(rule PricingRule, now time.Time) bool {
	// 星期过滤
	if len(rule.Days) > 0 {
		wd := dayName(now)
		if !weekDaySet(rule.Days)[wd] {
			return false
		}
	}

	// 时间区间匹配
	if rule.StartTime != "" && rule.EndTime != "" {
		return timeWithinRange(now, rule.StartTime, rule.EndTime)
	}

	return true
}

// matchTokenTier 匹配 Token 阶梯规则
func matchTokenTier(rule PricingRule, promptTokens, totalTokens int) bool {
	// 如果两个条件都设置了，取 AND
	if rule.PromptMaxTokens > 0 && rule.ContextMaxTokens > 0 {
		return promptTokens <= rule.PromptMaxTokens && totalTokens <= rule.ContextMaxTokens
	}
	// 仅 prompt 条件
	if rule.PromptMaxTokens > 0 {
		return promptTokens <= rule.PromptMaxTokens
	}
	// 仅 context 条件
	if rule.ContextMaxTokens > 0 {
		return totalTokens <= rule.ContextMaxTokens
	}
	return true
}
