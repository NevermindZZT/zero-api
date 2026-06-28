package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
)

type ChannelHandler struct {
	channelRepo *store.ChannelRepo
	modelRepo   *store.ModelRepo
}

func NewChannelHandler(channelRepo *store.ChannelRepo, modelRepo *store.ModelRepo) *ChannelHandler {
	return &ChannelHandler{channelRepo: channelRepo, modelRepo: modelRepo}
}

// ListChannels 获取渠道列表
func (h *ChannelHandler) ListChannels(c *gin.Context) {
	channels, err := h.channelRepo.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if channels == nil {
		channels = []store.Channel{}
	}
	c.JSON(http.StatusOK, channels)
}

// GetChannel 获取单个渠道
func (h *ChannelHandler) GetChannel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ch, err := h.channelRepo.GetByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "渠道不存在"})
		return
	}
	c.JSON(http.StatusOK, ch)
}

// CreateChannel 创建渠道
func (h *ChannelHandler) CreateChannel(c *gin.Context) {
	var ch store.Channel
	if err := c.ShouldBindJSON(&ch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if ch.Status == "" {
		ch.Status = "active"
	}
	id, err := h.channelRepo.Create(&ch)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ch.ID = id
	c.JSON(http.StatusCreated, ch)
}

// UpdateChannel 更新渠道
func (h *ChannelHandler) UpdateChannel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var ch store.Channel
	if err := c.ShouldBindJSON(&ch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ch.ID = id
	if err := h.channelRepo.Update(&ch); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, ch)
}

// DeleteChannel 删除渠道
func (h *ChannelHandler) DeleteChannel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.channelRepo.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
