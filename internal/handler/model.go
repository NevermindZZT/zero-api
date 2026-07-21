package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
)

type ModelHandler struct {
	modelRepo *store.ModelRepo
}

func NewModelHandler(modelRepo *store.ModelRepo) *ModelHandler {
	return &ModelHandler{modelRepo: modelRepo}
}

// ListModels 获取模型列表
func (h *ModelHandler) ListModels(c *gin.Context) {
	channelID, _ := strconv.ParseInt(c.Query("channel_id"), 10, 64)
	models, err := h.modelRepo.List(channelID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if models == nil {
		models = []store.Model{}
	}
	c.JSON(http.StatusOK, models)
}

// GetModel 获取单个模型
func (h *ModelHandler) GetModel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	m, err := h.modelRepo.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "模型不存在"})
		return
	}
	c.JSON(http.StatusOK, m)
}

// UpdateModel 更新模型
func (h *ModelHandler) UpdateModel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var m store.Model
	if err := c.ShouldBindJSON(&m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	m.ID = id
	if err := h.modelRepo.Update(&m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.modelRepo.InvalidateModelCache()
	c.JSON(http.StatusOK, m)
}

// DeleteModel 删除模型
func (h *ModelHandler) DeleteModel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.modelRepo.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.modelRepo.InvalidateModelCache()
	c.Status(http.StatusNoContent)
}

// ToggleModel 启用/禁用模型
func (h *ModelHandler) ToggleModel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.modelRepo.ToggleStatus(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.modelRepo.InvalidateModelCache()
	m, err := h.modelRepo.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "模型不存在"})
		return
	}
	c.JSON(http.StatusOK, m)
}

// BatchAction 批量操作模型
func (h *ModelHandler) BatchAction(c *gin.Context) {
	var req struct {
		Action string  `json:"action"` // enable, disable, delete, reset, batch_edit
		IDs    []int64 `json:"ids"`
		// batch_edit 专用字段
		PricingInput      *float64 `json:"pricing_input,omitempty"`
		PricingOutput     *float64 `json:"pricing_output,omitempty"`
		PricingCacheRead  *float64 `json:"pricing_cache_read,omitempty"`
		PricingCacheWrite *float64 `json:"pricing_cache_write,omitempty"`
		ContextWindow     *int     `json:"context_window,omitempty"`
		MaxOutputTokens   *int     `json:"max_output_tokens,omitempty"`
		SupportsVision    *bool    `json:"supports_vision,omitempty"`
		SupportsThinking  *bool    `json:"supports_thinking,omitempty"`
		SupportsTools     *bool    `json:"supports_tools,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供操作类型和模型ID列表"})
		return
	}

	var err error
	switch req.Action {
	case "enable":
		err = h.modelRepo.BatchSetStatus(req.IDs, "active")
	case "disable":
		err = h.modelRepo.BatchSetStatus(req.IDs, "inactive")
	case "delete":
		err = h.modelRepo.BatchDelete(req.IDs)
	case "reset":
		err = h.modelRepo.BatchClearUserModified(req.IDs)
	case "batch_edit":
		err = h.modelRepo.BatchEdit(req.IDs, store.BatchEditFields{
			PricingInput:      req.PricingInput,
			PricingOutput:     req.PricingOutput,
			PricingCacheRead:  req.PricingCacheRead,
			PricingCacheWrite: req.PricingCacheWrite,
			ContextWindow:     req.ContextWindow,
			MaxOutputTokens:   req.MaxOutputTokens,
			SupportsVision:    req.SupportsVision,
			SupportsThinking:  req.SupportsThinking,
			SupportsTools:     req.SupportsTools,
		})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的操作: " + req.Action})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.modelRepo.InvalidateModelCache()
	c.JSON(http.StatusOK, gin.H{"affected": len(req.IDs)})
}

// ExportModels 导出所有模型为 JSON
func (h *ModelHandler) ExportModels(c *gin.Context) {
	data, err := h.modelRepo.ExportJSON()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	filename := "models-export-" + time.Now().Format("2006-01-02") + ".json"
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Data(http.StatusOK, "application/json; charset=utf-8", data)
}

// ImportModels 从 JSON 批量导入模型（按 model_id 匹配，标记 user_modified=1）
func (h *ModelHandler) ImportModels(c *gin.Context) {
	var req struct {
		OverwriteUserModified bool                   `json:"overwrite_user_modified"`
		Models                []store.ModelExportItem `json:"models"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "解析导入数据失败: " + err.Error()})
		return
	}
	if len(req.Models) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "导入数据为空"})
		return
	}

	count, err := h.modelRepo.ImportJSON(req.Models, req.OverwriteUserModified)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.modelRepo.InvalidateModelCache()
	c.JSON(http.StatusOK, gin.H{"imported": count})
}
