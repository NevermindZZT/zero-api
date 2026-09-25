package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/metrics"
	"github.com/never/zero-api/internal/store"
)

type UsageHandler struct {
	usageRepo *store.UsageRepo
	tps       *metrics.TPSMonitor
}

func NewUsageHandler(usageRepo *store.UsageRepo, tps *metrics.TPSMonitor) *UsageHandler {
	return &UsageHandler{usageRepo: usageRepo, tps: tps}
}

// parseTzOffset 解析客户端时区偏移（分钟），默认 480（UTC+8）
func parseTzOffset(c *gin.Context) int {
	s := c.DefaultQuery("tz_offset", "480")
	v, err := strconv.Atoi(s)
	if err != nil {
		return 480
	}
	return v
}

// GetOverview 获取总览统计
func (h *UsageHandler) GetOverview(c *gin.Context) {
	apiKeyID := c.Query("api_key_id")
	start := c.Query("start")
	end := c.Query("end")
	tz := parseTzOffset(c)
	stats, err := h.usageRepo.GetOverview(apiKeyID, start, end, tz)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

// GetDailyStats 获取按日/月/小时统计（含补0）
func (h *UsageHandler) GetDailyStats(c *gin.Context) {
	start := c.DefaultQuery("start", time.Now().AddDate(0, -7, 0).Format("2006-01-02"))
	end := c.DefaultQuery("end", time.Now().Format("2006-01-02"))
	apiKeyID := c.Query("api_key_id")
	granularity := c.DefaultQuery("granularity", "day") // day | month | hour
	tz := parseTzOffset(c)

	stats, err := h.usageRepo.GetDailyStats(start, end, apiKeyID, granularity, tz)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if stats == nil {
		stats = []store.DailyStats{}
	}
	c.JSON(http.StatusOK, stats)
}

// GetRecentRecords 获取最近使用记录
func (h *UsageHandler) GetRecentRecords(c *gin.Context) {
	limit := 200
	if limitStr := c.Query("limit"); limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 2000 {
			limit = v
		}
	}
	apiKeyIDStr := c.Query("api_key_id")
	start := c.Query("start")
	end := c.Query("end")
	tz := parseTzOffset(c)
	records, err := h.usageRepo.GetRecentRecords(limit, apiKeyIDStr, start, end, tz)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if records == nil {
		records = []store.UsageRecord{}
	}
	c.JSON(http.StatusOK, records)
}

// GetTPS 获取进程内最近流式请求的模型 TPS 聚合（不落库）
func (h *UsageHandler) GetTPS(c *gin.Context) {
	windowName := c.DefaultQuery("window", "5m")
	var window time.Duration
	switch windowName {
	case "1m":
		window = time.Minute
	case "5m":
		window = 5 * time.Minute
	case "15m":
		window = 15 * time.Minute
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "window 仅支持 1m、5m 或 15m"})
		return
	}

	limit := 10
	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 50 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit 必须在 1 到 50 之间"})
			return
		}
		limit = value
	}

	items := h.tps.Snapshot(window, limit)
	if items == nil {
		items = []metrics.TPSModelStats{}
	}
	c.JSON(http.StatusOK, gin.H{
		"window_seconds": int(window.Seconds()),
		"items":          items,
	})
}

// GetModelStats 获取按模型聚合统计（饼图专用）
func (h *UsageHandler) GetModelStats(c *gin.Context) {
	start := c.DefaultQuery("start", time.Now().AddDate(0, -3, 0).Format("2006-01-02"))
	end := c.DefaultQuery("end", time.Now().Format("2006-01-02"))
	apiKeyID := c.Query("api_key_id")
	tz := parseTzOffset(c)

	stats, err := h.usageRepo.GetModelStats(start, end, apiKeyID, tz)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if stats == nil {
		stats = []store.ModelStats{}
	}
	c.JSON(http.StatusOK, stats)
}

// GetByAPIKey 按 API Key 统计
func (h *UsageHandler) GetByAPIKey(c *gin.Context) {
	apiKeyIDStr := c.Query("api_key_id")
	start := c.Query("start")
	end := c.Query("end")
	tz := parseTzOffset(c)
	stats, err := h.usageRepo.GetOverview(apiKeyIDStr, start, end, tz)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

// GetYearHeatmap 获取年度热力图数据（GitHub-style）
func (h *UsageHandler) GetYearHeatmap(c *gin.Context) {
	tz := parseTzOffset(c)
	data, err := h.usageRepo.GetYearHeatmapData(tz)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if data == nil {
		data = []store.YearHeatmapItem{}
	}
	c.JSON(http.StatusOK, data)
}
