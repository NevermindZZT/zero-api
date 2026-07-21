package store

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// tzLoc 根据客户端偏移分钟数构造时区
func tzLoc(minutes int) *time.Location {
	return time.FixedZone("Client", minutes*60)
}

// sqlTz 构造 SQLite strftime 时区修饰符，如 "+480 minutes"
func sqlTz(minutes int) string {
	return fmt.Sprintf("%+d minutes", minutes)
}

// localDateToUTCRange 将客户端时区的日期转为 UTC 时间范围（用于 usage_records 查询）
func localDateToUTCRange(date string, tzMinutes int) (utcStart, utcEnd string) {
	loc := tzLoc(tzMinutes)
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return date, addDay(date)
	}
	return t.UTC().Format("2006-01-02 15:04:05"),
		t.AddDate(0, 0, 1).UTC().Format("2006-01-02 15:04:05")
}

// localDateRangeToBounds 将客户端时区的起止日期转为 usage_daily 的 UTC 日期范围
// 返回 minDate, maxDate，使用 date >= ? AND date <= ? 查询
func localDateRangeToBounds(start, end string, tzMinutes int) (string, string) {
	loc := tzLoc(tzMinutes)
	startT, err := time.ParseInLocation("2006-01-02", start, loc)
	if err != nil {
		return start, end
	}
	endT, err := time.ParseInLocation("2006-01-02", end, loc)
	if err != nil {
		return start, end
	}
	return startT.UTC().Format("2006-01-02"),
		endT.AddDate(0, 0, 1).UTC().Format("2006-01-02")
}

// localNowDate 返回客户端时区的当天日期
func localNowDate(tzMinutes int) string {
	return time.Now().In(tzLoc(tzMinutes)).Format("2006-01-02")
}

// ===== 批量写入缓冲区 =====

var usageBuffer = make(chan *UsageRecord, 2000)
var bufferOnce sync.Once

// InitUsageBuffer 启动批量写入后台协程（在数据库初始化后调用）
func InitUsageBuffer(db *DB) {
	bufferOnce.Do(func() {
		go flushUsageLoop(db)
	})
}

func flushUsageLoop(db *DB) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Usage] 批量写入协程 panic 恢复，重启协程: %v", r)
			time.Sleep(3 * time.Second)
			go flushUsageLoop(db)
		}
	}()

	batch := make([]*UsageRecord, 0, 100)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case record := <-usageBuffer:
			batch = append(batch, record)
			if len(batch) >= 100 {
				flushUsageBatch(db, batch)
				batch = make([]*UsageRecord, 0, 100)
			}
		case <-ticker.C:
			if len(batch) > 0 {
				flushUsageBatch(db, batch)
				batch = make([]*UsageRecord, 0, 100)
			}
		}
	}
}

func flushUsageBatch(db *DB, batch []*UsageRecord) {
	tx, err := db.Begin()
	if err != nil {
		log.Printf("[Usage] 批量写入开始事务失败: %v", err)
		return
	}
	stmt, err := tx.Prepare(`INSERT INTO usage_records
		(channel_id, model_id, api_key_id, request_model,
		 prompt_tokens, completion_tokens, cache_hit_tokens, total_tokens,
		 latency_ms, cost)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		log.Printf("[Usage] 批量写入预编译失败: %v", err)
		return
	}
	defer stmt.Close()

	for _, u := range batch {
		if _, err := stmt.Exec(u.ChannelID, u.ModelID, u.APIKeyID, u.RequestModel,
			u.PromptTokens, u.CompletionTokens, u.CacheHitTokens, u.TotalTokens,
			u.LatencyMs, u.Cost); err != nil {
			log.Printf("[Usage] 批量写入单条失败: %v", err)
		}
	}

	// 更新预聚合表 usage_daily
	aggStmt, err := tx.Prepare(`INSERT INTO usage_daily
		(date, api_key_id, request_model, prompt_tokens, completion_tokens, cache_hit_tokens, total_tokens, requests, cost)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT(date, api_key_id, request_model) DO UPDATE SET
			prompt_tokens = prompt_tokens + excluded.prompt_tokens,
			completion_tokens = completion_tokens + excluded.completion_tokens,
			cache_hit_tokens = cache_hit_tokens + excluded.cache_hit_tokens,
			total_tokens = total_tokens + excluded.total_tokens,
			requests = requests + 1,
			cost = cost + excluded.cost`)
	if err != nil {
		tx.Rollback()
		log.Printf("[Usage] 预聚合插入预编译失败: %v", err)
		return
	}
	defer aggStmt.Close()

	for _, u := range batch {
		date := aggDate(u)
		apiKeyID := interface{}(nil)
		if u.APIKeyID != nil {
			apiKeyID = *u.APIKeyID
		}
		if _, err := aggStmt.Exec(date, apiKeyID, u.RequestModel,
			u.PromptTokens, u.CompletionTokens, u.CacheHitTokens, u.TotalTokens, u.Cost); err != nil {
			log.Printf("[Usage] 预聚合更新失败 (date=%s, model=%s): %v", date, u.RequestModel, err)
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[Usage] 批量写入提交事务失败: %v", err)
	}
}

// aggDate 获取预聚合用日期（UTC 日期），用于 usage_daily 存储
func aggDate(u *UsageRecord) string {
	if u.CreatedAt.IsZero() {
		return time.Now().UTC().Format("2006-01-02")
	}
	return u.CreatedAt.UTC().Format("2006-01-02")
}

// ===== UsageRecord & Repo =====
type UsageRecord struct {
	ID               int64     `json:"id"`
	ChannelID        *int64    `json:"channel_id,omitempty"`
	ChannelName      string    `json:"channel_name,omitempty"`
	ModelID          *int64    `json:"model_id,omitempty"`
	APIKeyID         *int64    `json:"api_key_id,omitempty"`
	APIKeyName       string    `json:"api_key_name,omitempty"`
	RequestModel     string    `json:"request_model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	CacheHitTokens   int       `json:"cache_hit_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	LatencyMs        int       `json:"latency_ms"`
	Cost             float64   `json:"cost"`
	CreatedAt        time.Time `json:"created_at"`
}

// DailyStats 按日聚合统计
type DailyStats struct {
	Date             string  `json:"date"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CacheHitTokens   int     `json:"cache_hit_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Requests         int     `json:"requests"`
	Cost             float64 `json:"cost"`
}

// OverviewStats 总览统计
type OverviewStats struct {
	TotalRequests    int64   `json:"total_requests"`
	TotalTokens      int64   `json:"total_tokens"`
	TotalCost        float64 `json:"total_cost"`
	ActiveChannels   int     `json:"active_channels"`
	ActiveModels     int     `json:"active_models"`
	TodayTokens      int64   `json:"today_tokens"`
	TodayRequests    int64   `json:"today_requests"`
	TotalCacheHits   int64   `json:"total_cache_hits"`
	CacheHitRate     float64 `json:"cache_hit_rate"`
}

type UsageRepo struct {
	db *DB
}

func NewUsageRepo(db *DB) *UsageRepo {
	return &UsageRepo{db: db}
}

// Insert 记录一条使用记录（通过缓冲区批量写入，不阻塞请求）
func (r *UsageRepo) Insert(u *UsageRecord) (int64, error) {
	select {
	case usageBuffer <- u:
	default:
		// 缓冲区满，直接写入（不应发生，2000 缓冲应足够）
		log.Printf("[Usage] 缓冲队列已满，直接写入")
		return r.insertDirect(u)
	}
	return 0, nil
}

// insertDirect 直接写入（绕过缓冲区）
func (r *UsageRepo) insertDirect(u *UsageRecord) (int64, error) {
	result, err := r.db.Exec(
		`INSERT INTO usage_records (channel_id, model_id, api_key_id, request_model, prompt_tokens, completion_tokens, cache_hit_tokens, total_tokens, latency_ms, cost)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ChannelID, u.ModelID, u.APIKeyID, u.RequestModel, u.PromptTokens, u.CompletionTokens, u.CacheHitTokens, u.TotalTokens, u.LatencyMs, u.Cost,
	)
	if err != nil {
		return 0, err
	}

	// 同步更新预聚合表
	r.updateDailyAgg(u)

	return result.LastInsertId()
}

// updateDailyAgg 更新 usage_daily 预聚合（单条记录）
func (r *UsageRepo) updateDailyAgg(u *UsageRecord) {
	date := aggDate(u)
	apiKeyID := interface{}(nil)
	if u.APIKeyID != nil {
		apiKeyID = *u.APIKeyID
	}
	_, err := r.db.Exec(
		`INSERT INTO usage_daily (date, api_key_id, request_model, prompt_tokens, completion_tokens, cache_hit_tokens, total_tokens, requests, cost)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)
		 ON CONFLICT(date, api_key_id, request_model) DO UPDATE SET
			prompt_tokens = prompt_tokens + excluded.prompt_tokens,
			completion_tokens = completion_tokens + excluded.completion_tokens,
			cache_hit_tokens = cache_hit_tokens + excluded.cache_hit_tokens,
			total_tokens = total_tokens + excluded.total_tokens,
			requests = requests + 1,
			cost = cost + excluded.cost`,
		date, apiKeyID, u.RequestModel, u.PromptTokens, u.CompletionTokens, u.CacheHitTokens, u.TotalTokens, u.Cost,
	)
	if err != nil {
		log.Printf("[Usage] 直接写入预聚合更新失败: %v", err)
	}
}

// GetOverview 获取总览统计（基于预聚合表 usage_daily）
func (r *UsageRepo) GetOverview(apiKeyID, startDate, endDate string, tzOffsetMinutes int) (*OverviewStats, error) {
	stats := &OverviewStats{}
	args := []interface{}{}
	conditions := []string{}
	if apiKeyID != "" {
		conditions = append(conditions, "api_key_id = ?")
		args = append(args, apiKeyID)
	}
	if startDate != "" && endDate != "" {
		minDate, maxDate := localDateRangeToBounds(startDate, endDate, tzOffsetMinutes)
		conditions = append(conditions, "date >= ? AND date <= ?")
		args = append(args, minDate, maxDate)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + joinConditions(conditions)
	}

	// 从预聚合表查询：最多 365 行/年，远快于全表扫描 usage_records
	err := r.db.QueryRow(`SELECT
		COALESCE(SUM(requests), 0),
		COALESCE(SUM(total_tokens), 0),
		COALESCE(SUM(cost), 0),
		COALESCE(SUM(cache_hit_tokens), 0)
		FROM usage_daily`+where, args...).Scan(
		&stats.TotalRequests, &stats.TotalTokens, &stats.TotalCost, &stats.TotalCacheHits,
	)
	if err != nil {
		// 降级：表可能不存在（刚迁移），回退到 usage_records
		return r.getOverviewFallback(apiKeyID, startDate, endDate, tzOffsetMinutes)
	}

	// 安全兜底：usage_daily 返回 0 但 usage_records 有数据时自动降级
	if stats.TotalRequests == 0 && stats.TotalTokens == 0 {
		var usageRecCount int64
		_ = r.db.QueryRow("SELECT COUNT(*) FROM usage_records").Scan(&usageRecCount)
		if usageRecCount > 0 {
			log.Printf("[Usage] usage_daily 为空但 usage_records 有 %d 条记录，自动触发回填+降级", usageRecCount)
			go r.backfillDailyAgg()
			return r.getOverviewFallback(apiKeyID, startDate, endDate, tzOffsetMinutes)
		}
	}

	if stats.TotalTokens > 0 {
		stats.CacheHitRate = float64(stats.TotalCacheHits) / float64(stats.TotalTokens) * 100
	}

	// 合并活跃渠道/模型数到一次查询
	r.db.QueryRow(
		`SELECT
			(SELECT COUNT(*) FROM channels WHERE status = 'active'),
			(SELECT COUNT(*) FROM models WHERE status = 'active')`,
	).Scan(&stats.ActiveChannels, &stats.ActiveModels)

	// 今日统计（客户端时区当天）
	today := localNowDate(tzOffsetMinutes)
	todayMin, todayMax := localDateRangeToBounds(today, today, tzOffsetMinutes)
	todayArgs := []interface{}{todayMin, todayMax}
	todayWhere := " WHERE date >= ? AND date <= ?"
	if apiKeyID != "" {
		todayWhere += " AND api_key_id = ?"
		todayArgs = append(todayArgs, apiKeyID)
	}
	err = r.db.QueryRow(
		`SELECT COALESCE(SUM(requests), 0), COALESCE(SUM(total_tokens), 0) FROM usage_daily`+todayWhere,
		todayArgs...,
	).Scan(&stats.TodayRequests, &stats.TodayTokens)
	if err != nil {
		// 降级：可能是 usage_daily 表还不存在
		stats.TodayRequests = 0
		stats.TodayTokens = 0
	}

	return stats, nil
}

// getOverviewFallback 降级方案：从 usage_records 全表查询
func (r *UsageRepo) getOverviewFallback(apiKeyID, startDate, endDate string, tzOffsetMinutes int) (*OverviewStats, error) {
	stats := &OverviewStats{}
	args := []interface{}{}
	conditions := []string{}
	if apiKeyID != "" {
		conditions = append(conditions, "api_key_id = ?")
		args = append(args, apiKeyID)
	}
	if startDate != "" && endDate != "" {
		utcStart, _ := localDateToUTCRange(startDate, tzOffsetMinutes)
		_, utcEnd := localDateToUTCRange(endDate, tzOffsetMinutes)
		conditions = append(conditions, "created_at >= ? AND created_at < ?")
		args = append(args, utcStart, utcEnd)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + joinConditions(conditions)
	}

	err := r.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(total_tokens), 0), COALESCE(SUM(cost), 0), COALESCE(SUM(cache_hit_tokens), 0) FROM usage_records`+where, args...).Scan(
		&stats.TotalRequests, &stats.TotalTokens, &stats.TotalCost, &stats.TotalCacheHits,
	)
	if err != nil {
		return nil, err
	}
	if stats.TotalTokens > 0 {
		stats.CacheHitRate = float64(stats.TotalCacheHits) / float64(stats.TotalTokens) * 100
	}

	r.db.QueryRow(
		`SELECT
			(SELECT COUNT(*) FROM channels WHERE status = 'active'),
			(SELECT COUNT(*) FROM models WHERE status = 'active')`,
	).Scan(&stats.ActiveChannels, &stats.ActiveModels)

	// 今日统计（客户端时区当天 UTC 00:00）
	todayLoc := time.Now().In(tzLoc(tzOffsetMinutes))
	todayUTCStart := todayLoc.Truncate(24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	todayArgs := []interface{}{todayUTCStart}
	todayWhere := ""
	if apiKeyID != "" {
		todayWhere = " AND api_key_id = ?"
		todayArgs = append(todayArgs, apiKeyID)
	}
	err = r.db.QueryRow(
		`SELECT COALESCE(COUNT(*), 0), COALESCE(SUM(total_tokens), 0) FROM usage_records WHERE created_at >= ?`+todayWhere,
		todayArgs...,
	).Scan(&stats.TodayRequests, &stats.TodayTokens)
	if err != nil {
		stats.TodayRequests = 0
		stats.TodayTokens = 0
	}

	return stats, nil
}

// GetDailyStats 获取按日/月/小时聚合统计（含补0；hour 粒度直接查 usage_records）
func (r *UsageRepo) GetDailyStats(start, end string, apiKeyID string, granularity string, tzOffsetMinutes int) ([]DailyStats, error) {
	if granularity == "hour" {
		return r.getHourlyStats(start, apiKeyID, tzOffsetMinutes)
	}

	var dateExpr, groupBy, orderBy string
	isMonth := granularity == "month"
	if isMonth {
		dateExpr = "substr(date, 1, 7)"
		groupBy = "substr(date, 1, 7)"
		orderBy = "substr(date, 1, 7) DESC"
	} else {
		dateExpr = "date"
		groupBy = "date"
		orderBy = "date DESC"
	}

	minDate, maxDate := localDateRangeToBounds(start, end, tzOffsetMinutes)
	whereDate := "WHERE date >= ? AND date <= ?"
	args := []interface{}{minDate, maxDate}
	if apiKeyID != "" {
		whereDate += " AND api_key_id = ?"
		args = append(args, apiKeyID)
	}
	rows, err := r.db.Query(
		`SELECT `+dateExpr+`,
		        COALESCE(SUM(prompt_tokens), 0),
		        COALESCE(SUM(completion_tokens), 0),
		        COALESCE(SUM(cache_hit_tokens), 0),
		        COALESCE(SUM(total_tokens), 0),
		        COALESCE(SUM(requests), 0),
		        COALESCE(SUM(cost), 0)
		 FROM usage_daily `+whereDate+`
		 GROUP BY `+groupBy+` ORDER BY `+orderBy, args...)
	if err != nil {
		// 降级：回退到 usage_records 查询
		return r.getDailyStatsFallback(start, end, granularity, apiKeyID, tzOffsetMinutes)
	}
	defer rows.Close()

	dataMap := make(map[string]DailyStats)
	for rows.Next() {
		var s DailyStats
		if err := rows.Scan(&s.Date, &s.PromptTokens, &s.CompletionTokens, &s.CacheHitTokens, &s.TotalTokens, &s.Requests, &s.Cost); err != nil {
			return nil, err
		}
		dataMap[s.Date] = s
	}

	if len(dataMap) == 0 {
		var usageRecCount int64
		_ = r.db.QueryRow("SELECT COUNT(*) FROM usage_records").Scan(&usageRecCount)
		if usageRecCount > 0 {
			log.Printf("[Usage] GetDailyStats: usage_daily 为空但 usage_records 有 %d 条记录，自动触发回填+降级", usageRecCount)
			go r.backfillDailyAgg()
			return r.getDailyStatsFallback(start, end, granularity, apiKeyID, tzOffsetMinutes)
		}
	}

	return fillZeroStats(start, end, dataMap, granularity), nil
}

// fillZeroStats 生成完整日期/月份/小时序列，无数据项补 0
func fillZeroStats(start, end string, dataMap map[string]DailyStats, granularity string) []DailyStats {
	var stats []DailyStats
	switch granularity {
	case "month":
		startMonth := start[:7]
		endMonth := end[:7]
		startT, _ := time.Parse("2006-01", startMonth)
		endT, _ := time.Parse("2006-01", endMonth)
		for t := endT; !t.Before(startT); t = t.AddDate(0, -1, 0) {
			key := t.Format("2006-01")
			if s, ok := dataMap[key]; ok {
				stats = append(stats, s)
			} else {
				stats = append(stats, DailyStats{Date: key})
			}
		}
	case "hour":
		// 生成 00~23 共 24 小时，key 格式 "2025-07-21 08"
		for h := 23; h >= 0; h-- {
			key := start + " " + fmt.Sprintf("%02d", h)
			if s, ok := dataMap[key]; ok {
				stats = append(stats, s)
			} else {
				stats = append(stats, DailyStats{Date: key})
			}
		}
	default: // "day"
		startT, _ := time.Parse("2006-01-02", start)
		endT, _ := time.Parse("2006-01-02", end)
		for t := endT; !t.Before(startT); t = t.AddDate(0, 0, -1) {
			key := t.Format("2006-01-02")
			if s, ok := dataMap[key]; ok {
				stats = append(stats, s)
			} else {
				stats = append(stats, DailyStats{Date: key})
			}
		}
	}
	return stats
}

// getHourlyStats 按小时统计（直接查 usage_records，不使用 usage_daily）
func (r *UsageRepo) getHourlyStats(date string, apiKeyID string, tzOffsetMinutes int) ([]DailyStats, error) {
	utcStart, utcEnd := localDateToUTCRange(date, tzOffsetMinutes)
	tzMod := sqlTz(tzOffsetMinutes)

	whereDate := "WHERE created_at >= ? AND created_at < ?"
	args := []interface{}{utcStart, utcEnd}
	if apiKeyID != "" {
		whereDate += " AND api_key_id = ?"
		args = append(args, apiKeyID)
	}
	rows, err := r.db.Query(
		`SELECT strftime('%Y-%m-%d %H', created_at, '`+tzMod+`') as hour,
		        COALESCE(SUM(prompt_tokens), 0),
		        COALESCE(SUM(completion_tokens), 0),
		        COALESCE(SUM(cache_hit_tokens), 0),
		        COALESCE(SUM(total_tokens), 0),
		        COUNT(*),
		        COALESCE(SUM(cost), 0)
		 FROM usage_records `+whereDate+`
		 GROUP BY hour ORDER BY hour DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	dataMap := make(map[string]DailyStats)
	for rows.Next() {
		var s DailyStats
		if err := rows.Scan(&s.Date, &s.PromptTokens, &s.CompletionTokens, &s.CacheHitTokens, &s.TotalTokens, &s.Requests, &s.Cost); err != nil {
			return nil, err
		}
		dataMap[s.Date] = s
	}

	return fillZeroStats(date, "", dataMap, "hour"), nil
}

// getDailyStatsFallback 降级方案：从 usage_records 实时聚合
func (r *UsageRepo) getDailyStatsFallback(start, end, granularity, apiKeyID string, tzOffsetMinutes int) ([]DailyStats, error) {
	tzMod := sqlTz(tzOffsetMinutes)
	var dateExpr, orderBy string
	switch granularity {
	case "month":
		dateExpr = "substr(created_at, 1, 7)"
		orderBy = "substr(created_at, 1, 7) DESC"
	case "hour":
		dateExpr = "strftime('%Y-%m-%d %H', created_at, '" + tzMod + "')"
		orderBy = "strftime('%Y-%m-%d %H', created_at, '" + tzMod + "') DESC"
	default:
		dateExpr = "date(created_at, '" + tzMod + "')"
		orderBy = "date(created_at, '" + tzMod + "') DESC"
	}

	utcStart, _ := localDateToUTCRange(start, tzOffsetMinutes)
	_, utcEnd := localDateToUTCRange(end, tzOffsetMinutes)
	whereDate := "WHERE created_at >= ? AND created_at < ?"
	args := []interface{}{utcStart, utcEnd}
	if apiKeyID != "" {
		whereDate += " AND api_key_id = ?"
		args = append(args, apiKeyID)
	}
	rows, err := r.db.Query(
		`SELECT `+dateExpr+` as day,
		        COALESCE(SUM(prompt_tokens), 0),
		        COALESCE(SUM(completion_tokens), 0),
		        COALESCE(SUM(cache_hit_tokens), 0),
		        COALESCE(SUM(total_tokens), 0),
		        COUNT(*),
		        COALESCE(SUM(cost), 0)
		 FROM usage_records `+whereDate+`
		 GROUP BY day ORDER BY `+orderBy, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	dataMap := make(map[string]DailyStats)
	for rows.Next() {
		var s DailyStats
		if err := rows.Scan(&s.Date, &s.PromptTokens, &s.CompletionTokens, &s.CacheHitTokens, &s.TotalTokens, &s.Requests, &s.Cost); err != nil {
			return nil, err
		}
		dataMap[s.Date] = s
	}

	return fillZeroStats(start, end, dataMap, granularity), nil
}

// ModelStats 按模型聚合统计
type ModelStats struct {
	RequestModel string  `json:"request_model"`
	TotalTokens  int64   `json:"total_tokens"`
	Requests     int64   `json:"requests"`
	Cost         float64 `json:"cost"`
}

// GetModelStats 获取按模型聚合统计（饼图专用）
func (r *UsageRepo) GetModelStats(start, end string, apiKeyID string, tzOffsetMinutes int) ([]ModelStats, error) {
	stats, err := r.getModelStatsFromDaily(start, end, apiKeyID, tzOffsetMinutes)
	if err != nil {
		return nil, err
	}

	// 安全兜底：usage_daily 无数据但 usage_records 有数据时自动降级
	if len(stats) == 0 {
		var usageRecCount int64
		_ = r.db.QueryRow("SELECT COUNT(*) FROM usage_records").Scan(&usageRecCount)
		if usageRecCount > 0 {
			log.Printf("[Usage] GetModelStats: usage_daily 为空但 usage_records 有 %d 条记录，自动触发回填+降级", usageRecCount)
			go r.backfillDailyAgg()
			return r.getModelStatsFallback(start, end, apiKeyID, tzOffsetMinutes)
		}
	}

	return stats, nil
}

func (r *UsageRepo) getModelStatsFromDaily(start, end string, apiKeyID string, tzOffsetMinutes int) ([]ModelStats, error) {
	minDate, maxDate := localDateRangeToBounds(start, end, tzOffsetMinutes)
	whereDate := "WHERE date >= ? AND date <= ?"
	args := []interface{}{minDate, maxDate}
	if apiKeyID != "" {
		whereDate += " AND api_key_id = ?"
		args = append(args, apiKeyID)
	}
	rows, err := r.db.Query(
		`SELECT request_model,
		        COALESCE(SUM(total_tokens), 0),
		        COALESCE(SUM(requests), 0),
		        COALESCE(SUM(cost), 0)
		 FROM usage_daily `+whereDate+`
		 GROUP BY request_model ORDER BY total_tokens DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []ModelStats
	for rows.Next() {
		var s ModelStats
		if err := rows.Scan(&s.RequestModel, &s.TotalTokens, &s.Requests, &s.Cost); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, nil
}

// GetRecentRecords 获取最近使用记录
func (r *UsageRepo) GetRecentRecords(limit int, apiKeyID, startDate, endDate string, tzOffsetMinutes int) ([]UsageRecord, error) {
	args := []interface{}{}
	conditions := []string{}
	if apiKeyID != "" {
		conditions = append(conditions, "u.api_key_id = ?")
		args = append(args, apiKeyID)
	}
	if startDate != "" {
		utcStart, _ := localDateToUTCRange(startDate, tzOffsetMinutes)
		conditions = append(conditions, "u.created_at >= ?")
		args = append(args, utcStart)
	}
	if endDate != "" {
		_, utcEnd := localDateToUTCRange(endDate, tzOffsetMinutes)
		conditions = append(conditions, "u.created_at < ?")
		args = append(args, utcEnd)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + joinConditions(conditions)
	}
	args = append(args, limit)
	rows, err := r.db.Query(
		`SELECT u.id, u.channel_id, u.model_id, u.api_key_id, u.request_model,
		        u.prompt_tokens, u.completion_tokens, u.cache_hit_tokens, u.total_tokens,
		        u.latency_ms, u.cost, u.created_at,
		        COALESCE(ak.name, '') AS api_key_name,
		        COALESCE(c.name, '') AS channel_name
		 FROM usage_records u
		 LEFT JOIN api_keys ak ON u.api_key_id = ak.id
		 LEFT JOIN channels c ON u.channel_id = c.id`+where+` ORDER BY u.created_at DESC LIMIT ?`, args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 检查是否有迭代错误
	var records []UsageRecord
	for rows.Next() {
		var u UsageRecord
		if err := rows.Scan(&u.ID, &u.ChannelID, &u.ModelID, &u.APIKeyID, &u.RequestModel,
			&u.PromptTokens, &u.CompletionTokens, &u.CacheHitTokens, &u.TotalTokens,
			&u.LatencyMs, &u.Cost, &u.CreatedAt, &u.APIKeyName, &u.ChannelName); err != nil {
			return nil, err
		}
		records = append(records, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// backfillDailyAgg 异步回填 usage_daily（从 usage_records 聚合，使用本地时间）
func (r *UsageRepo) backfillDailyAgg() {
	log.Println("[Usage] 开始异步回填 usage_daily...")
	// 先清空旧数据（旧数据可能是 UTC 日期），再重新聚合
	if _, err := r.db.Exec(`DELETE FROM usage_daily`); err != nil {
		log.Printf("[Usage] 清空 usage_daily 失败: %v", err)
	}
	result, err := r.db.Exec(`
		INSERT INTO usage_daily (date, api_key_id, request_model, prompt_tokens, completion_tokens, cache_hit_tokens, total_tokens, requests, cost)
		SELECT date(created_at), api_key_id, request_model,
		       SUM(prompt_tokens), SUM(completion_tokens), SUM(cache_hit_tokens), SUM(total_tokens),
		       COUNT(*), SUM(cost)
		FROM usage_records
		GROUP BY date(created_at), api_key_id, request_model`)
	if err != nil {
		log.Printf("[Usage] 异步回填 usage_daily 失败: %v", err)
		return
	}
	affected, _ := result.RowsAffected()
	log.Printf("[Usage] 异步回填 usage_daily 完成，写入 %d 行聚合数据", affected)
}

// BackfillDailyAgg 公开的回填方法（服务启动时调用）
func (r *UsageRepo) BackfillDailyAgg() {
	r.backfillDailyAgg()
}

// getModelStatsFallback 降级方案：从 usage_records 实时按模型聚合
func (r *UsageRepo) getModelStatsFallback(start, end string, apiKeyID string, tzOffsetMinutes int) ([]ModelStats, error) {
	utcStart, _ := localDateToUTCRange(start, tzOffsetMinutes)
	_, utcEnd := localDateToUTCRange(end, tzOffsetMinutes)
	whereDate := "WHERE created_at >= ? AND created_at < ?"
	args := []interface{}{utcStart, utcEnd}
	if apiKeyID != "" {
		whereDate += " AND api_key_id = ?"
		args = append(args, apiKeyID)
	}
	rows, err := r.db.Query(
		`SELECT request_model,
		        COALESCE(SUM(total_tokens), 0),
		        COALESCE(COUNT(*), 0),
		        COALESCE(SUM(cost), 0)
		 FROM usage_records `+whereDate+`
		 GROUP BY request_model ORDER BY total_tokens DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []ModelStats
	for rows.Next() {
		var s ModelStats
		if err := rows.Scan(&s.RequestModel, &s.TotalTokens, &s.Requests, &s.Cost); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, nil
}

// joinConditions 拼接 SQL WHERE 条件
func joinConditions(conds []string) string {
	result := ""
	for i, c := range conds {
		if i > 0 {
			result += " AND "
		}
		result += c
	}
	return result
}

// addDay 将日期字符串加一天，用于范围查询结束边界
func addDay(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}

// YearHeatmapItem 年度热力图数据（每日 total_tokens）
type YearHeatmapItem struct {
	Date        string `json:"date"`
	TotalTokens int64  `json:"total_tokens"`
}

// GetYearHeatmapData 获取过去一年每日 Tokens 用量
func (r *UsageRepo) GetYearHeatmapData(tzOffsetMinutes int) ([]YearHeatmapItem, error) {
	end := time.Now().In(tzLoc(tzOffsetMinutes))
	start := end.AddDate(-1, 0, 0)
	startStr := start.Format("2006-01-02")
	endStr := end.Format("2006-01-02")

	rows, err := r.db.Query(
		`SELECT date, COALESCE(SUM(total_tokens), 0)
		 FROM usage_daily
		 WHERE date >= ? AND date <= ?
		 GROUP BY date ORDER BY date`, startStr, endStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 先读到 map 中
	dataMap := make(map[string]int64)
	for rows.Next() {
		var item YearHeatmapItem
		if err := rows.Scan(&item.Date, &item.TotalTokens); err != nil {
			return nil, err
		}
		dataMap[item.Date] = item.TotalTokens
	}

	// 生成完整 365 天序列，无数据的日期补 0
	var items []YearHeatmapItem
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		tokens := dataMap[dateStr]
		items = append(items, YearHeatmapItem{Date: dateStr, TotalTokens: tokens})
	}
	return items, nil
}
