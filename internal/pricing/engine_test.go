package pricing

import (
	"testing"
	"time"
)

// fixedTime 构造固定时间用于测试
func fixedTime(hour, min int) time.Time {
	return time.Date(2026, 7, 19, hour, min, 0, 0, time.UTC)
}

func fixedTimeWithWeekday(weekday time.Weekday, hour, min int) time.Time {
	// 找一个指定星期几的日期
	base := time.Date(2026, 7, 20, hour, min, 0, 0, time.UTC) // 2026-07-20 = Monday
	diff := int(weekday - time.Monday)
	return base.AddDate(0, 0, diff)
}

// ===== Rule Validation =====

func TestValidateRules_OK(t *testing.T) {
	rules := PricingRules{
		{ID: "off-peak", Type: RuleTypeTimeRange, Enabled: true, Name: "低谷",
			Days: []string{"mon", "tue", "wed", "thu", "fri"},
			StartTime: "00:00", EndTime: "08:00",
			PricingInput: 0.35, PricingOutput: 0.70},
		{ID: "large-context", Type: RuleTypeTokenTier, Enabled: true, Name: "超大上下文",
			ContextMaxTokens: 1000000,
			PricingInput: 3.48, PricingOutput: 6.96},
	}
	if err := rules.Validate(); err != nil {
		t.Fatalf("期望无错误，得到: %v", err)
	}
}

func TestValidateRules_DuplicateID(t *testing.T) {
	rules := PricingRules{
		{ID: "rule1", Type: RuleTypeTimeRange, Enabled: true,
			StartTime: "00:00", EndTime: "08:00"},
		{ID: "rule1", Type: RuleTypeTimeRange, Enabled: true,
			StartTime: "08:00", EndTime: "18:00"},
	}
	if err := rules.Validate(); err == nil {
		t.Fatal("期望重复 ID 错误")
	}
}

func TestValidateRules_InvalidTimeFormat(t *testing.T) {
	rules := PricingRules{
		{ID: "bad-time", Type: RuleTypeTimeRange, Enabled: true,
			StartTime: "25:00", EndTime: "08:00"},
	}
	if err := rules.Validate(); err == nil {
		t.Fatal("期望时间格式错误")
	}
}

func TestValidateRules_InvalidDay(t *testing.T) {
	rules := PricingRules{
		{ID: "bad-day", Type: RuleTypeTimeRange, Enabled: true,
			Days: []string{"xxx"}, StartTime: "00:00", EndTime: "08:00"},
	}
	if err := rules.Validate(); err == nil {
		t.Fatal("期望无效星期值错误")
	}
}

func TestValidateRules_TokenTierNoThreshold(t *testing.T) {
	rules := PricingRules{
		{ID: "no-threshold", Type: RuleTypeTokenTier, Enabled: true,
			PricingInput: 1.0},
	}
	if err := rules.Validate(); err == nil {
		t.Fatal("期望缺少阈值错误")
	}
}

func TestValidateRules_EmptyID(t *testing.T) {
	rules := PricingRules{
		{ID: "", Type: RuleTypeTimeRange, Enabled: true,
			StartTime: "00:00", EndTime: "08:00"},
	}
	if err := rules.Validate(); err == nil {
		t.Fatal("期望空 ID 错误")
	}
}

func TestValidateRules_UnknownType(t *testing.T) {
	rules := PricingRules{
		{ID: "unknown", Type: "unknown", Enabled: true},
	}
	if err := rules.Validate(); err == nil {
		t.Fatal("期望未知类型错误")
	}
}

// ===== ParsePricingRules =====

func TestParsePricingRules_Empty(t *testing.T) {
	rules, err := ParsePricingRules("")
	if err != nil {
		t.Fatalf("期望无错误: %v", err)
	}
	if rules != nil {
		t.Fatalf("期望 nil，得到 %v", rules)
	}
}

func TestParsePricingRules_JSON(t *testing.T) {
	json := `[{"id":"r1","type":"time_range","enabled":true,"start_time":"00:00","end_time":"08:00","pricing_input":0.5}]`
	rules, err := ParsePricingRules(json)
	if err != nil {
		t.Fatalf("期望无错误: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("期望 1 条规则，得到 %d", len(rules))
	}
	if rules[0].ID != "r1" {
		t.Fatalf("期望 ID=r1，得到 %s", rules[0].ID)
	}
}

func TestParsePricingRules_InvalidJSON(t *testing.T) {
	_, err := ParsePricingRules("{invalid}")
	if err == nil {
		t.Fatal("期望解析失败")
	}
}

func TestMustParsePricingRules(t *testing.T) {
	rules := MustParsePricingRules("")
	if len(rules) != 0 {
		t.Fatalf("期望空列表，得到 %d", len(rules))
	}

	rules = MustParsePricingRules(`[{"id":"r1","type":"time_range","enabled":true}]`)
	if len(rules) != 1 {
		t.Fatalf("期望 1 条，得到 %d", len(rules))
	}
}

func TestPricingRules_String(t *testing.T) {
	var rules PricingRules
	if s := rules.String(); s != "[]" {
		t.Fatalf("期望 []，得到 %s", s)
	}

	rules = PricingRules{{ID: "r1", Type: RuleTypeTimeRange}}
	s := rules.String()
	if s == "[]" {
		t.Fatal("不应该返回空数组")
	}
}

// ===== ResolvePricing =====

func TestResolvePricing_NoRules(t *testing.T) {
	flat := PricingSet{Input: 1.0, Output: 2.0, CacheRead: 0.1, CacheWrite: 0.5}
	matched, resolved := ResolvePricing(nil, flat, time.Now(), 100, 200)
	if matched != "" {
		t.Fatalf("期望空匹配 ID，得到 %s", matched)
	}
	if resolved != flat {
		t.Fatalf("期望 flat 定价，得到 %+v", resolved)
	}
}

func TestResolvePricing_EmptyRules(t *testing.T) {
	flat := PricingSet{Input: 1.0, Output: 2.0, CacheRead: 0.1, CacheWrite: 0.5}
	matched, resolved := ResolvePricing(PricingRules{}, flat, time.Now(), 100, 200)
	if matched != "" {
		t.Fatalf("期望空匹配 ID，得到 %s", matched)
	}
	if resolved != flat {
		t.Fatalf("期望 flat 定价，得到 %+v", resolved)
	}
}

func TestResolvePricing_TimeRange_Match(t *testing.T) {
	rules := PricingRules{
		{ID: "off-peak", Type: RuleTypeTimeRange, Enabled: true,
			Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
			StartTime: "00:00", EndTime: "08:00",
			PricingInput: 0.35, PricingOutput: 0.70},
	}
	flat := PricingSet{Input: 1.0, Output: 2.0, CacheRead: 0.1, CacheWrite: 0.5}

	// 时间在 00:00-08:00 内
	now := fixedTime(3, 0) // 03:00
	matched, resolved := ResolvePricing(rules, flat, now, 100, 200)
	if matched != "off-peak" {
		t.Fatalf("期望匹配 off-peak，得到 %s", matched)
	}
	if resolved.Input != 0.35 || resolved.Output != 0.70 {
		t.Fatalf("期望定价 0.35/0.70，得到 %f/%f", resolved.Input, resolved.Output)
	}
}

func TestResolvePricing_TimeRange_NoMatch_OutsideWindow(t *testing.T) {
	rules := PricingRules{
		{ID: "off-peak", Type: RuleTypeTimeRange, Enabled: true,
			Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
			StartTime: "00:00", EndTime: "08:00",
			PricingInput: 0.35, PricingOutput: 0.70},
	}
	flat := PricingSet{Input: 1.0, Output: 2.0, CacheRead: 0.1, CacheWrite: 0.5}

	now := fixedTime(12, 0) // 12:00，不在时段内
	matched, resolved := ResolvePricing(rules, flat, now, 100, 200)
	if matched != "" {
		t.Fatalf("期望无匹配，得到 %s", matched)
	}
	if resolved != flat {
		t.Fatalf("期望 flat 定价")
	}
}

func TestResolvePricing_TimeRange_CrossMidnight(t *testing.T) {
	rules := PricingRules{
		{ID: "night", Type: RuleTypeTimeRange, Enabled: true,
			Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
			StartTime: "22:00", EndTime: "06:00",
			PricingInput: 0.20, PricingOutput: 0.40},
	}
	flat := PricingSet{Input: 1.0, Output: 2.0, CacheRead: 0.1, CacheWrite: 0.5}

	// 23:00 在跨天区间内
	now := fixedTime(23, 0)
	matched, resolved := ResolvePricing(rules, flat, now, 100, 200)
	if matched != "night" {
		t.Fatalf("期望匹配 night，得到 %s", matched)
	}
	if resolved.Input != 0.20 {
		t.Fatalf("期望 Input=0.20，得到 %f", resolved.Input)
	}

	// 03:00 也在跨天区间内
	now = fixedTime(3, 0)
	matched, resolved = ResolvePricing(rules, flat, now, 100, 200)
	if matched != "night" {
		t.Fatalf("期望匹配 night，得到 %s", matched)
	}

	// 12:00 不在区间内
	now = fixedTime(12, 0)
	matched, resolved = ResolvePricing(rules, flat, now, 100, 200)
	if matched != "" {
		t.Fatalf("期望无匹配，得到 %s", matched)
	}
}

func TestResolvePricing_TimeRange_WeekdayFilter(t *testing.T) {
	rules := PricingRules{
		{ID: "weekend", Type: RuleTypeTimeRange, Enabled: true,
			Days: []string{"sat", "sun"},
			StartTime: "00:00", EndTime: "23:59",
			PricingInput: 0.5, PricingOutput: 1.0},
	}
	flat := PricingSet{Input: 1.0, Output: 2.0, CacheRead: 0.1, CacheWrite: 0.5}

	// 周六
	now := fixedTimeWithWeekday(time.Saturday, 12, 0)
	matched, _ := ResolvePricing(rules, flat, now, 100, 200)
	if matched != "weekend" {
		t.Fatalf("周末应匹配，得到 %s", matched)
	}

	// 周三
	now = fixedTimeWithWeekday(time.Wednesday, 12, 0)
	matched, _ = ResolvePricing(rules, flat, now, 100, 200)
	if matched != "" {
		t.Fatalf("工作日不应匹配，得到 %s", matched)
	}
}

func TestResolvePricing_TokenTier_Prompt(t *testing.T) {
	rules := PricingRules{
		{ID: "small-prompt", Type: RuleTypeTokenTier, Enabled: true,
			PromptMaxTokens: 4096,
			PricingInput: 0.5, PricingOutput: 1.0},
	}
	flat := PricingSet{Input: 2.0, Output: 4.0, CacheRead: 0.2, CacheWrite: 0.8}

	// prompt = 3000 <= 4096，应匹配
	matched, resolved := ResolvePricing(rules, flat, time.Now(), 3000, 5000)
	if matched != "small-prompt" {
		t.Fatalf("期望匹配 small-prompt，得到 %s", matched)
	}
	if resolved.Input != 0.5 {
		t.Fatalf("期望 Input=0.5，得到 %f", resolved.Input)
	}

	// prompt = 5000 > 4096，不应匹配
	matched, resolved = ResolvePricing(rules, flat, time.Now(), 5000, 6000)
	if matched != "" {
		t.Fatalf("期望无匹配，得到 %s", matched)
	}
	if resolved != flat {
		t.Fatalf("期望 flat 定价")
	}
}

func TestResolvePricing_TokenTier_Context(t *testing.T) {
	rules := PricingRules{
		{ID: "small-context", Type: RuleTypeTokenTier, Enabled: true,
			ContextMaxTokens: 8000,
			PricingInput: 0.3, PricingOutput: 0.6},
	}
	flat := PricingSet{Input: 1.0, Output: 2.0}

	matched, resolved := ResolvePricing(rules, flat, time.Now(), 3000, 7000)
	if matched != "small-context" {
		t.Fatalf("期望匹配 small-context，得到 %s", matched)
	}
	if resolved.Input != 0.3 {
		t.Fatalf("期望 Input=0.3，得到 %f", resolved.Input)
	}

	// total = 9000 > 8000
	matched, resolved = ResolvePricing(rules, flat, time.Now(), 3000, 9000)
	if matched != "" {
		t.Fatalf("期望无匹配，得到 %s", matched)
	}
}

func TestResolvePricing_TokenTier_AND(t *testing.T) {
	rules := PricingRules{
		{ID: "small-both", Type: RuleTypeTokenTier, Enabled: true,
			PromptMaxTokens: 4096, ContextMaxTokens: 8192,
			PricingInput: 0.2, PricingOutput: 0.4},
	}
	flat := PricingSet{Input: 1.0, Output: 2.0}

	// 两者都符合
	matched, _ := ResolvePricing(rules, flat, time.Now(), 3000, 7000)
	if matched != "small-both" {
		t.Fatalf("期望匹配，得到 %s", matched)
	}

	// prompt 超出
	matched, _ = ResolvePricing(rules, flat, time.Now(), 5000, 7000)
	if matched != "" {
		t.Fatalf("期望无匹配（prompt 超出），得到 %s", matched)
	}

	// context 超出
	matched, _ = ResolvePricing(rules, flat, time.Now(), 3000, 9000)
	if matched != "" {
		t.Fatalf("期望无匹配（context 超出），得到 %s", matched)
	}
}

func TestResolvePricing_DisabledRuleSkipped(t *testing.T) {
	rules := PricingRules{
		{ID: "disabled-rule", Type: RuleTypeTimeRange, Enabled: false,
			Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
			StartTime: "00:00", EndTime: "23:59",
			PricingInput: 0.01, PricingOutput: 0.01},
	}
	flat := PricingSet{Input: 1.0, Output: 2.0}

	matched, resolved := ResolvePricing(rules, flat, time.Now(), 100, 200)
	if matched != "" {
		t.Fatalf("禁用的规则不应匹配，得到 %s", matched)
	}
	if resolved != flat {
		t.Fatalf("期望 flat 定价")
	}
}

func TestResolvePricing_PriorityOrder(t *testing.T) {
	// 第一条规则应优先匹配
	rules := PricingRules{
		{ID: "high-priority", Type: RuleTypeTimeRange, Enabled: true,
			Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
			StartTime: "00:00", EndTime: "23:59",
			PricingInput: 0.1, PricingOutput: 0.2},
		{ID: "low-priority", Type: RuleTypeTimeRange, Enabled: true,
			Days: []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"},
			StartTime: "00:00", EndTime: "23:59",
			PricingInput: 0.5, PricingOutput: 1.0},
	}
	flat := PricingSet{Input: 2.0, Output: 4.0}

	matched, resolved := ResolvePricing(rules, flat, time.Now(), 100, 200)
	if matched != "high-priority" {
		t.Fatalf("期望 high-priority，得到 %s", matched)
	}
	if resolved.Input != 0.1 {
		t.Fatalf("期望 Input=0.1，得到 %f", resolved.Input)
	}
}

// ===== timeWithinRange =====

func TestTimeWithinRange_SameDay(t *testing.T) {
	if !timeWithinRange(fixedTime(3, 0), "00:00", "08:00") {
		t.Fatal("03:00 应在 00:00-08:00 内")
	}
	if timeWithinRange(fixedTime(10, 0), "00:00", "08:00") {
		t.Fatal("10:00 不应在 00:00-08:00 内")
	}
	if !timeWithinRange(fixedTime(0, 0), "00:00", "08:00") {
		t.Fatal("00:00 应在区间内（边界）")
	}
	if !timeWithinRange(fixedTime(8, 0), "00:00", "08:00") {
		t.Fatal("08:00 应在区间内（边界）")
	}
}

func TestTimeWithinRange_CrossMidnight(t *testing.T) {
	if !timeWithinRange(fixedTime(23, 30), "22:00", "06:00") {
		t.Fatal("23:30 应在跨天区间 22:00-06:00 内")
	}
	if !timeWithinRange(fixedTime(3, 0), "22:00", "06:00") {
		t.Fatal("03:00 应在跨天区间 22:00-06:00 内")
	}
	if timeWithinRange(fixedTime(12, 0), "22:00", "06:00") {
		t.Fatal("12:00 不应在跨天区间内")
	}
	if !timeWithinRange(fixedTime(22, 0), "22:00", "06:00") {
		t.Fatal("22:00 应在区间内（边界）")
	}
	if !timeWithinRange(fixedTime(6, 0), "22:00", "06:00") {
		t.Fatal("06:00 应在区间内（边界）")
	}
}

func TestTimeWithinRange_AllDay(t *testing.T) {
	// 00:00-00:00 = 全天
	if !timeWithinRange(fixedTime(12, 0), "00:00", "00:00") {
		t.Fatal("全天任意时间应匹配")
	}
}
