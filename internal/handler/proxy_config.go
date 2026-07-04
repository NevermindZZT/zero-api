package handler

import (
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
)

type ProxyConfigHandler struct {
	proxyConfigRepo *store.ProxyConfigRepo
	certDir         string
	onUpdate        func() // 配置更新后的回调（用于通知 ProxyHandler 刷新缓存）
}

func NewProxyConfigHandler(proxyConfigRepo *store.ProxyConfigRepo, certDir string) *ProxyConfigHandler {
	return &ProxyConfigHandler{proxyConfigRepo: proxyConfigRepo, certDir: certDir}
}

// SetOnUpdate 设置配置更新后的回调
func (h *ProxyConfigHandler) SetOnUpdate(fn func()) {
	h.onUpdate = fn
}

// GetConfig 获取代理配置
func (h *ProxyConfigHandler) GetConfig(c *gin.Context) {
	cfg, err := h.proxyConfigRepo.Get()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// UpdateConfig 更新代理配置
func (h *ProxyConfigHandler) UpdateConfig(c *gin.Context) {
	var cfg store.ProxyConfigData
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	existing, err := h.proxyConfigRepo.Get()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	cfg.ID = existing.ID
	if err := h.proxyConfigRepo.Update(&cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 通知 ProxyHandler 刷新配置缓存
	if h.onUpdate != nil {
		h.onUpdate()
	}
	c.JSON(http.StatusOK, cfg)
}

// DownloadCert 下载根 CA 证书（根据格式参数）
func (h *ProxyConfigHandler) DownloadCert(c *gin.Context) {
	format := c.DefaultQuery("format", "pem")
	var fileName, certFileName string
	if format == "crt" {
		certFileName = "root-ca-cert.crt"
		fileName = "zero-api-root-ca.crt"
	} else {
		certFileName = "root-ca-cert.pem"
		fileName = "zero-api-root-ca.pem"
	}
	certPath := filepath.Join(h.certDir, certFileName)
	c.FileAttachment(certPath, fileName)
}
