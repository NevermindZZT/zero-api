package store

import (
	"log"
	"sync"
	"time"
)

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

	if err := tx.Commit(); err != nil {
		log.Printf("[Usage] 批量写入提交事务失败: %v", err)
	}
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
	return result.LastInsertId()
}

// GetOverview 获取总览统计
func (r *UsageRepo) GetOverview(apiKeyID ...string) (*OverviewStats, error) {
	stats := &OverviewStats{}
	args := []interface{}{}
	where := ""
	if len(apiKeyID) > 0 && apiKeyID[0] != "" {
		where = " WHERE api_key_id = ?"
		args = append(args, apiKeyID[0])
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

	// 合并活跃渠道/模型数到一次查询
	r.db.QueryRow(
		`SELECT
			(SELECT COUNT(*) FROM channels WHERE status = 'active'),
			(SELECT COUNT(*) FROM models WHERE status = 'active')`,
	).Scan(&stats.ActiveChannels, &stats.ActiveModels)

	// 今日统计：使用 created_at >= 范围查询（可利用 idx_usage_created_at 索引）
	todayArgs := []interface{}{}
	todayWhere := ""
	if len(apiKeyID) > 0 && apiKeyID[0] != "" {
		todayWhere = " AND api_key_id = ?"
		todayArgs = append(todayArgs, apiKeyID[0])
	}
	todayStart := time.Now().Format("2006-01-02")
	err = r.db.QueryRow(
		`SELECT COALESCE(COUNT(*), 0), COALESCE(SUM(total_tokens), 0) FROM usage_records WHERE created_at >= ?`+todayWhere,
		append([]interface{}{todayStart}, todayArgs...)...,
	).Scan(&stats.TodayRequests, &stats.TodayTokens)
	if err != nil {
		// 静默处理：可能是旧版数据中没有 created_at
		stats.TodayRequests = 0
		stats.TodayTokens = 0
	}

	return stats, nil
}

// GetDailyStats 获取按日聚合统计
func (r *UsageRepo) GetDailyStats(start, end string, apiKeyID ...string) ([]DailyStats, error) {
	// 使用 created_at >= ? AND created_at < ? 范围查询以利用 idx_usage_created_at 索引
	nextDay := addDay(end)
	whereDate := "WHERE created_at >= ? AND created_at < ?"
	args := []interface{}{start, nextDay}
	if len(apiKeyID) > 0 && apiKeyID[0] != "" {
		whereDate += " AND api_key_id = ?"
		args = append(args, apiKeyID[0])
	}
	rows, err := r.db.Query(
		`SELECT date(created_at) as day,
		        COALESCE(SUM(prompt_tokens), 0),
		        COALESCE(SUM(completion_tokens), 0),
		        COALESCE(SUM(cache_hit_tokens), 0),
		        COALESCE(SUM(total_tokens), 0),
		        COUNT(*),
		        COALESCE(SUM(cost), 0)
		 FROM usage_records `+whereDate+`
		 GROUP BY day ORDER BY day DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []DailyStats
	for rows.Next() {
		var s DailyStats
		if err := rows.Scan(&s.Date, &s.PromptTokens, &s.CompletionTokens, &s.CacheHitTokens, &s.TotalTokens, &s.Requests, &s.Cost); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, nil
}

// GetRecentRecords 获取最近使用记录
func (r *UsageRepo) GetRecentRecords(limit int, apiKeyID, startDate, endDate string) ([]UsageRecord, error) {
	args := []interface{}{}
	conditions := []string{}
	if apiKeyID != "" {
		conditions = append(conditions, "u.api_key_id = ?")
		args = append(args, apiKeyID)
	}
	if startDate != "" {
		conditions = append(conditions, "u.created_at >= ?")
		args = append(args, startDate)
	}
	if endDate != "" {
		conditions = append(conditions, "u.created_at < ?")
		args = append(args, addDay(endDate))
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
