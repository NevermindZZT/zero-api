package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
)

// ===== 虚拟模型 CRUD 管理 =====
// 虚拟模型的请求路由逻辑（识图扩展、模型名替换等）在 internal/router 包中，
// 由 ProxyHandler 的路由规则链（routers）调用，见 handler/proxy.go。

// VirtualModelHandler 虚拟模型（模型路由）管理
type VirtualModelHandler struct {
	repo     *store.VirtualModelRepo
	onUpdate func() // 数据变更后的回调（通知 ProxyHandler 刷新 /v1/models 缓存）
}

func NewVirtualModelHandler(repo *store.VirtualModelRepo) *VirtualModelHandler {
	return &VirtualModelHandler{repo: repo}
}

// SetOnUpdate 设置数据变更后的回调（虚拟模型 CRUD 后调用，刷新模型列表缓存）
func (h *VirtualModelHandler) SetOnUpdate(fn func()) {
	h.onUpdate = fn
}

// invalidateCache 数据变更后通知回调
func (h *VirtualModelHandler) invalidateCache() {
	if h.onUpdate != nil {
		h.onUpdate()
	}
}

func (h *VirtualModelHandler) List(c *gin.Context) {
	list, err := h.repo.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []store.VirtualModel{}
	}
	c.JSON(http.StatusOK, list)
}

func (h *VirtualModelHandler) Create(c *gin.Context) {
	var vm store.VirtualModel
	if err := c.ShouldBindJSON(&vm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if vm.Name == "" || vm.MainModel == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "虚拟模型名和主模型必填"})
		return
	}
	if vm.Status == "" {
		vm.Status = "active"
	}
	id, err := h.repo.Create(&vm)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.invalidateCache()
	vm.ID = id
	c.JSON(http.StatusCreated, vm)
}

func (h *VirtualModelHandler) Update(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var vm store.VirtualModel
	if err := c.ShouldBindJSON(&vm); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	vm.ID = id
	// status 未传（空）时保留原值：避免 PUT 部分字段更新把状态覆盖为空
	if vm.Status == "" {
		existing, err := h.repo.GetByID(id)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "虚拟模型不存在"})
			return
		}
		vm.Status = existing.Status
	}
	if err := h.repo.Update(&vm); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.invalidateCache()
	c.JSON(http.StatusOK, vm)
}

func (h *VirtualModelHandler) Delete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.repo.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.invalidateCache()
	c.Status(http.StatusNoContent)
}

func (h *VirtualModelHandler) Toggle(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.repo.ToggleStatus(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.invalidateCache()
	c.Status(http.StatusNoContent)
}
