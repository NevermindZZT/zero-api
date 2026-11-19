package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
	"github.com/never/zero-api/internal/upstream"
)

type SyncHandler struct {
	syncer      *upstream.Syncer
	modelRepo   *store.ModelRepo
	presetsPath string
	onSynced    func()
}

// SetOnSynced registers a cache invalidation callback for successful syncs.
func (h *SyncHandler) SetOnSynced(fn func()) { h.onSynced = fn }

func NewSyncHandler(syncer *upstream.Syncer, modelRepo *store.ModelRepo, presetsPath string) *SyncHandler {
	return &SyncHandler{syncer: syncer, modelRepo: modelRepo, presetsPath: presetsPath}
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

	// 同步完成，清除模型缓存
	h.modelRepo.InvalidateModelCache()
	if h.onSynced != nil {
		h.onSynced()
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "同步完成",
		"count":   count,
	})
}

// PreviewModels fetches and validates a complete upstream list without writing models.
func (h *SyncHandler) PreviewModels(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的渠道ID"})
		return
	}
	preview, err := h.syncer.PreviewModels(c.Request.Context(), id)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, upstream.ErrPreviewConflict) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, preview)
}

// ConfirmModels validates the opaque preview and atomically applies its selection.
func (h *SyncHandler) ConfirmModels(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的渠道ID"})
		return
	}
	var req struct {
		PreviewID        string    `json:"preview_id"`
		SelectedModelIDs *[]string `json:"selected_model_ids"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if err := c.ShouldBindJSON(&req); err != nil || req.PreviewID == "" || req.SelectedModelIDs == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "必须提供 preview_id 和 selected_model_ids 数组"})
		return
	}
	count, deleted, err := h.syncer.ConfirmModels(c.Request.Context(), id, req.PreviewID, *req.SelectedModelIDs)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, upstream.ErrPreviewConflict) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if h.onSynced != nil {
		h.onSynced()
	}
	c.JSON(http.StatusOK, gin.H{"count": count, "deleted_count": deleted})
}

// TestChannel 测试渠道连通性
func (h *SyncHandler) TestChannel(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "渠道连通性测试功能待实现"})
}

// ReloadPresets 重新加载模型预设文件
func (h *SyncHandler) ReloadPresets(c *gin.Context) {
	if err := h.syncer.ReloadPresets(h.presetsPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "预设重新加载成功"})
}
