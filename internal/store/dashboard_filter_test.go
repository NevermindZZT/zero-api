package store

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func addDashboardUsage(t *testing.T, db *DB, key interface{}, at string, prompt, output, cached, latency int) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO usage_records (api_key_id, request_model, prompt_tokens, completion_tokens, cache_hit_tokens, total_tokens, latency_ms, total_duration_ms, created_at) VALUES (?, 'm', ?, ?, ?, ?, ?, ?, ?)`, key, prompt, output, cached, prompt+output, latency, latency*2, at)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDashboardKeyFiltersAndCacheRate(t *testing.T) {
	db := setupStatsTestDB(t)
	addDashboardUsage(t, db, 101, "2026-08-03 00:00:00", 100, 100, 50, 10)
	addDashboardUsage(t, db, 102, "2026-08-03 01:00:00", 0, 100, 25, 20)
	repo := NewUsageRepo(db)
	first, err := repo.GetOverview("101", "2026-08-03", "2026-08-03", 480)
	if err != nil {
		t.Fatal(err)
	}
	if first.TotalRequests != 1 || first.TotalPromptTokens != 100 || first.CacheHitRate != 50 {
		t.Fatalf("wrong key 101 overview: %+v", first)
	}
	zero, err := repo.GetOverview("102", "2026-08-03", "2026-08-03", 480)
	if err != nil {
		t.Fatal(err)
	}
	if zero.CacheHitRate != 0 {
		t.Fatalf("zero input cache rate = %v", zero.CacheHitRate)
	}
	all, err := repo.GetOverview("", "2026-08-03", "2026-08-03", 480)
	if err != nil || all.TotalRequests != 6 {
		t.Fatalf("all overview = %+v, err=%v", all, err)
	}
	models, err := repo.GetModelStats("2026-08-03", "2026-08-03", "101", 480)
	if err != nil || len(models) != 1 || models[0].TotalTokens != 200 {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	records, err := repo.GetRecentRecords(200, "101", "2026-08-03", "2026-08-03", 480)
	if err != nil || len(records) != 1 || records[0].APIKeyID == nil || *records[0].APIKeyID != 101 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

func TestDashboardMultiDayHourlyAndLatencyBuckets(t *testing.T) {
	db := setupStatsTestDB(t)
	// Both timestamps are midnight in UTC+8, on two distinct local days.
	addDashboardUsage(t, db, 101, "2026-08-02 16:00:00", 10, 0, 0, 10)
	addDashboardUsage(t, db, 101, "2026-08-03 16:00:00", 20, 0, 0, 20)
	addDashboardUsage(t, db, 102, "2026-08-03 16:00:00", 99, 0, 0, 99)
	repo := NewUsageRepo(db)
	daily, err := repo.GetDailyStats("2026-08-03", "2026-08-05", "101", "hour", 480)
	if err != nil {
		t.Fatal(err)
	}
	latency, err := repo.GetLatencyStats("2026-08-03", "2026-08-05", "101", "hour", 480)
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) != 72 || len(latency) != 72 {
		t.Fatalf("expected 72 hourly buckets; daily=%d latency=%d", len(daily), len(latency))
	}
	if daily[0].Date != "2026-08-05 23" || daily[71].Date != "2026-08-03 00" {
		t.Fatalf("order: %s ... %s", daily[0].Date, daily[71].Date)
	}
	for i, expected := range map[int]int{71: 10, 47: 20} {
		if daily[i].TotalTokens != expected || latency[i].SampleCount != 1 || int(latency[i].LatencyP50Ms) != expected {
			t.Fatalf("bucket %d daily=%+v latency=%+v", i, daily[i], latency[i])
		}
	}
	if daily[0].TotalTokens != 0 || latency[0].SampleCount != 0 {
		t.Fatalf("empty bucket not zero-filled")
	}
}

func TestDashboardMonthlyLocalTimezone(t *testing.T) {
	db := setupStatsTestDB(t)
	addDashboardUsage(t, db, 101, "2026-07-31 16:00:00", 10, 0, 0, 0) // Aug 1 in UTC+8
	addDashboardUsage(t, db, 101, "2026-08-31 16:00:00", 20, 0, 0, 0) // Sep 1 in UTC+8
	stats, err := NewUsageRepo(db).GetDailyStats("2026-08-01", "2026-09-30", "101", "month", 480)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 || stats[0].Date != "2026-09" || stats[0].TotalTokens != 20 || stats[1].Date != "2026-08" || stats[1].TotalTokens != 10 {
		t.Fatalf("monthly buckets: %+v", stats)
	}
}

func TestDashboardHeatmapKeyFilterAndBoundaries(t *testing.T) {
	db := setupStatsTestDB(t)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	addDashboardUsage(t, db, 101, "2025-08-02 15:59:59", 1000, 0, 0, 0) // just before first local day
	addDashboardUsage(t, db, 101, "2025-08-02 16:00:00", 10, 0, 0, 0)   // first local day
	addDashboardUsage(t, db, 101, "2026-08-03 15:59:59", 20, 0, 0, 0)   // last local day
	addDashboardUsage(t, db, 101, "2026-08-03 16:00:00", 1000, 0, 0, 0) // exclusive end
	addDashboardUsage(t, db, 102, "2026-08-03 00:00:00", 99, 0, 0, 0)
	repo := NewUsageRepo(db)
	items, err := repo.getYearHeatmapDataAt(480, "101", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 366 || items[0].Date != "2025-08-03" || items[0].TotalTokens != 10 || items[len(items)-1].Date != "2026-08-03" || items[len(items)-1].TotalTokens != 20 {
		t.Fatalf("heatmap boundaries first=%+v last=%+v count=%d", items[0], items[len(items)-1], len(items))
	}
	all, err := repo.getYearHeatmapDataAt(480, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if all[len(all)-1].TotalTokens != 1119 {
		t.Fatalf("all-key final day = %d", all[len(all)-1].TotalTokens)
	}
}

func TestDashboardUsageKeyDateIndex(t *testing.T) {
	db := setupStatsTestDB(t)
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT * FROM usage_records WHERE api_key_id = ? AND created_at >= ? AND created_at < ? ORDER BY created_at DESC LIMIT 200`, 101, "2026-01-01", "2026-12-31")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += fmt.Sprintln(detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_usage_api_key_created_at") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("unexpected query plan: %s", plan)
	}
}
