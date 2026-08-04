package store

import (
	"path/filepath"
	"testing"
	"time"
)

// 测试今日统计边界：客户端时区（UTC+8）的"今日"跨两个 UTC 日期，
// 精确边界应返回 客户端今日 0 点对应的 UTC 时间 到 次日 0 点对应的 UTC 时间
func TestLocalDateToUTCRange_Today(t *testing.T) {
	// 客户端 UTC+8，今日 2026-08-04
	// 客户端 2026-08-04 00:00 (+08:00) = UTC 2026-08-03 16:00
	// 客户端 2026-08-05 00:00 (+08:00) = UTC 2026-08-04 16:00
	start, end := localDateToUTCRange("2026-08-04", 480)
	wantStart := "2026-08-03 16:00:00"
	wantEnd := "2026-08-04 16:00:00"
	if start != wantStart {
		t.Errorf("start 应为 %s，got %s", wantStart, start)
	}
	if end != wantEnd {
		t.Errorf("end 应为 %s，got %s", wantEnd, end)
	}
}

// 测试负数时区偏移（如美西 UTC-8）
func TestLocalDateToUTCRange_NegativeOffset(t *testing.T) {
	// 客户端 UTC-8（-480 分钟），今日 2026-08-04
	// 客户端 2026-08-04 00:00 (-08:00) = UTC 2026-08-04 08:00
	start, end := localDateToUTCRange("2026-08-04", -480)
	wantStart := "2026-08-04 08:00:00"
	wantEnd := "2026-08-05 08:00:00"
	if start != wantStart {
		t.Errorf("start 应为 %s，got %s", wantStart, start)
	}
	if end != wantEnd {
		t.Errorf("end 应为 %s，got %s", wantEnd, end)
	}
}

// 测试 UTC 时区（0 偏移）
func TestLocalDateToUTCRange_ZeroOffset(t *testing.T) {
	start, end := localDateToUTCRange("2026-08-04", 0)
	if start != "2026-08-04 00:00:00" || end != "2026-08-05 00:00:00" {
		t.Errorf("零偏移错误: %s ~ %s", start, end)
	}
}

// 测试 localNowDate 返回客户端时区当天日期
func TestLocalNowDate(t *testing.T) {
	loc := tzLoc(480)
	now := time.Now().In(loc)
	d := localNowDate(480)
	if d != now.Format("2006-01-02") {
		t.Errorf("localNowDate 应为 %s，got %s", now.Format("2006-01-02"), d)
	}
}

// setupStatsTestDB 创建临时数据库并插入跨时区边界测试数据
// 客户端 UTC+8，查询 2026-08-03（= UTC 2026-08-02 16:00 ~ 2026-08-03 16:00）
// 插入 4 条记录，分布在该时间窗口内（含 UTC 8/2 与 8/3 两天）
func setupStatsTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// 先插入渠道和模型（usage_records 有外键约束）
	if _, err := db.Exec(`INSERT INTO channels (name, type, base_url, api_key, status) VALUES ('test', 'openai', 'http://test', '', 'active')`); err != nil {
		t.Fatalf("插入测试渠道失败: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO models (channel_id, model_id, status) VALUES (1, 'test-model', 'active')`); err != nil {
		t.Fatalf("插入测试模型失败: %v", err)
	}

	// 客户端 8/3 的各时段记录（UTC 时间）
	records := []struct {
		createdAt string
		tokens    int
	}{
		{"2026-08-02 16:30:00", 100}, // 客户端 8/3 00:30
		{"2026-08-02 23:00:00", 200}, // 客户端 8/3 07:00
		{"2026-08-03 05:00:00", 300}, // 客户端 8/3 13:00
		{"2026-08-03 15:00:00", 400}, // 客户端 8/3 23:00
	}
	for _, r := range records {
		if _, err := db.Exec(`INSERT INTO usage_records
			(channel_id, model_id, api_key_id, request_model, prompt_tokens, completion_tokens,
			 cache_hit_tokens, total_tokens, latency_ms, total_duration_ms, cost, created_at)
			 VALUES (1, 1, NULL, 'test-model', ?, 0, 0, ?, 0, 0, 0, ?)`,
			r.tokens, r.tokens, r.createdAt); err != nil {
			t.Fatalf("插入测试数据失败: %v", err)
		}
	}
	return db
}

// 测试 GetOverview 在非 UTC 时区下的日期边界（不应包含前一天的客户端数据）
func TestGetOverview_TZBoundary(t *testing.T) {
	db := setupStatsTestDB(t)
	repo := NewUsageRepo(db)

	// 客户端 UTC+8 查询 8/3
	stats, err := repo.GetOverview("", "2026-08-03", "2026-08-03", 480)
	if err != nil {
		t.Fatalf("GetOverview 失败: %v", err)
	}
	if stats.TotalRequests != 4 {
		t.Errorf("请求数应为 4，got %d", stats.TotalRequests)
	}
	if stats.TotalTokens != 1000 {
		t.Errorf("总 tokens 应为 1000，got %d", stats.TotalTokens)
	}
}

// 测试 GetDailyStats 返回客户端时区日期标签（而非 UTC 日期）
func TestGetDailyStats_TZLabel(t *testing.T) {
	db := setupStatsTestDB(t)
	repo := NewUsageRepo(db)

	stats, err := repo.GetDailyStats("2026-08-03", "2026-08-03", "", "day", 480)
	if err != nil {
		t.Fatalf("GetDailyStats 失败: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("应有 1 天数据，got %d: %+v", len(stats), stats)
	}
	if stats[0].Date != "2026-08-03" {
		t.Errorf("日期标签应为客户端 2026-08-03，got %s（UTC 日期错位）", stats[0].Date)
	}
	if stats[0].TotalTokens != 1000 || stats[0].Requests != 4 {
		t.Errorf("聚合错误: tokens=%d requests=%d", stats[0].TotalTokens, stats[0].Requests)
	}
}

// 测试 GetModelStats 在非 UTC 时区下的边界
func TestGetModelStats_TZBoundary(t *testing.T) {
	db := setupStatsTestDB(t)
	repo := NewUsageRepo(db)

	stats, err := repo.GetModelStats("2026-08-03", "2026-08-03", "", 480)
	if err != nil {
		t.Fatalf("GetModelStats 失败: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("应有 1 个模型，got %d: %+v", len(stats), stats)
	}
	if stats[0].RequestModel != "test-model" || stats[0].TotalTokens != 1000 || stats[0].Requests != 4 {
		t.Errorf("聚合错误: %+v", stats[0])
	}
}

// 测试：查询 UTC 8/2（客户端 8/2）时不应包含 UTC 8/2 16:00 之后的数据（属于客户端 8/3）
func TestGetOverview_ExcludeNextDay(t *testing.T) {
	db := setupStatsTestDB(t)
	repo := NewUsageRepo(db)

	// 客户端 UTC+8 查询 8/2（= UTC 8/1 16:00 ~ 8/2 16:00）
	stats, err := repo.GetOverview("", "2026-08-02", "2026-08-02", 480)
	if err != nil {
		t.Fatalf("GetOverview 失败: %v", err)
	}
	// 4 条记录都在 UTC 8/2 16:00 之后（客户端 8/3），不应被计入客户端 8/2
	if stats.TotalRequests != 0 || stats.TotalTokens != 0 {
		t.Errorf("客户端 8/2 应为 0，got requests=%d tokens=%d（边界泄漏）", stats.TotalRequests, stats.TotalTokens)
	}
}
