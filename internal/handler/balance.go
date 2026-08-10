package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/balance"
	"github.com/never/zero-api/internal/store"
)

// BalanceHandler 余额/订阅状态管理
type BalanceHandler struct {
	service *balance.Service
}

func NewBalanceHandler(service *balance.Service) *BalanceHandler {
	return &BalanceHandler{service: service}
}

// ListBalances 获取所有渠道余额
func (h *BalanceHandler) ListBalances(c *gin.Context) {
	list, err := h.service.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []store.ChannelBalance{}
	}
	c.JSON(http.StatusOK, list)
}

// GetBalance 获取单个渠道余额
func (h *BalanceHandler) GetBalance(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	cb, err := h.service.GetByChannel(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cb == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "该渠道暂无余额记录"})
		return
	}
	c.JSON(http.StatusOK, cb)
}

// RefreshBalance 刷新单个渠道余额
func (h *BalanceHandler) RefreshBalance(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	cb, err := h.service.Refresh(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cb)
}

// RefreshAllBalances 批量刷新所有启用渠道
func (h *BalanceHandler) RefreshAllBalances(c *gin.Context) {
	results := h.service.RefreshAll()
	if results == nil {
		results = []store.ChannelBalance{}
	}
	c.JSON(http.StatusOK, results)
}

// SetManualBalance 手动设置渠道余额（用于无公开余额 API 的供应商，如 OpenCode）
func (h *BalanceHandler) SetManualBalance(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Balance  float64 `json:"balance"`
		Currency string  `json:"currency"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cb, err := h.service.SetManualBalance(id, req.Balance, req.Currency)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cb)
}

// ListProviders 列出可用余额查询适配器
func (h *BalanceHandler) ListProviders(c *gin.Context) {
	c.JSON(http.StatusOK, h.service.Providers())
}
