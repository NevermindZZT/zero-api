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
	repo *store.VirtualModelRepo
}

func NewVirtualModelHandler(repo *store.VirtualModelRepo) *VirtualModelHandler {
	return &VirtualModelHandler{repo: repo}
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
	if err := h.repo.Update(&vm); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, vm)
}

func (h *VirtualModelHandler) Delete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.repo.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *VirtualModelHandler) Toggle(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.repo.ToggleStatus(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
