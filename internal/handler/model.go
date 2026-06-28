package handler

import (
	"net/http"
	"strconv"

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
	c.JSON(http.StatusOK, m)
}

// DeleteModel 删除模型
func (h *ModelHandler) DeleteModel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.modelRepo.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ToggleModel 启用/禁用模型
func (h *ModelHandler) ToggleModel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	m, err := h.modelRepo.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "模型不存在"})
		return
	}
	if m.Status == "active" {
		m.Status = "inactive"
	} else {
		m.Status = "active"
	}
	if err := h.modelRepo.Update(m); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, m)
}

// BatchAction 批量操作模型
func (h *ModelHandler) BatchAction(c *gin.Context) {
	var req struct {
		Action string  `json:"action"` // enable, disable, delete
		IDs    []int64 `json:"ids"`
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
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的操作: " + req.Action})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"affected": len(req.IDs)})
}
