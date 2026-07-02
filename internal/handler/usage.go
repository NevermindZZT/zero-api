package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
)

type UsageHandler struct {
	usageRepo *store.UsageRepo
}

func NewUsageHandler(usageRepo *store.UsageRepo) *UsageHandler {
	return &UsageHandler{usageRepo: usageRepo}
}

// GetOverview 获取总览统计
func (h *UsageHandler) GetOverview(c *gin.Context) {
	apiKeyID := c.Query("api_key_id")
	stats, err := h.usageRepo.GetOverview(apiKeyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

// GetDailyStats 获取按日统计
func (h *UsageHandler) GetDailyStats(c *gin.Context) {
	start := c.DefaultQuery("start", time.Now().AddDate(0, -7, 0).Format("2006-01-02"))
	end := c.DefaultQuery("end", time.Now().Format("2006-01-02"))
	apiKeyID := c.Query("api_key_id")

	stats, err := h.usageRepo.GetDailyStats(start, end, apiKeyID)
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
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 && v <= 50000 {
			limit = v
		}
	}
	apiKeyIDStr := c.Query("api_key_id")
	start := c.Query("start")
	end := c.Query("end")
	records, err := h.usageRepo.GetRecentRecords(limit, apiKeyIDStr, start, end)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if records == nil {
		records = []store.UsageRecord{}
	}
	c.JSON(http.StatusOK, records)
}

// GetByAPIKey 按 API Key 统计
func (h *UsageHandler) GetByAPIKey(c *gin.Context) {
	apiKeyIDStr := c.Query("api_key_id")
	stats, err := h.usageRepo.GetOverview(apiKeyIDStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}
