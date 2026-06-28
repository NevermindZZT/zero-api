package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/upstream"
)

type SyncHandler struct {
	syncer *upstream.Syncer
}

func NewSyncHandler(syncer *upstream.Syncer) *SyncHandler {
	return &SyncHandler{syncer: syncer}
}

// SyncModels 从上游同步模型列表
func (h *SyncHandler) SyncModels(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的渠道ID"})
		return
	}

	count, err := h.syncer.SyncModels(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "同步完成",
		"count":   count,
	})
}

// TestChannel 测试渠道连通性
func (h *SyncHandler) TestChannel(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "渠道连通性测试功能待实现"})
}
