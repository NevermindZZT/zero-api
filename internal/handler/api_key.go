package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
)

type APIKeyHandler struct {
	apiKeyRepo *store.APIKeyRepo
}

func NewAPIKeyHandler(apiKeyRepo *store.APIKeyRepo) *APIKeyHandler {
	return &APIKeyHandler{apiKeyRepo: apiKeyRepo}
}

func (h *APIKeyHandler) List(c *gin.Context) {
	keys, err := h.apiKeyRepo.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if keys == nil {
		keys = []store.APIKey{}
	}
	c.JSON(http.StatusOK, keys)
}

func (h *APIKeyHandler) Create(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供密钥名称"})
		return
	}
	key, err := h.apiKeyRepo.Create(req.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// API Key 变更，清除缓存
	h.apiKeyRepo.InvalidateAPIKeyCache()
	c.JSON(http.StatusCreated, key)
}

func (h *APIKeyHandler) Toggle(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.apiKeyRepo.Toggle(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *APIKeyHandler) Delete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.apiKeyRepo.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.apiKeyRepo.InvalidateAPIKeyCache()
	c.Status(http.StatusNoContent)
}
