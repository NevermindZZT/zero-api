package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/cpa"
	"github.com/never/zero-api/internal/store"
)

// CPAHandler CLIProxyAPI sidecar 管理
type CPAHandler struct {
	cfgRepo *store.CPAConfigRepo
	manager *cpa.Manager
	quota   *cpa.QuotaService
}

type loginRequest struct {
	Provider  string `json:"provider"`
	Device    bool   `json:"device"`
	NoBrowser bool   `json:"no_browser"`
}

func NewCPAHandler(cfgRepo *store.CPAConfigRepo, manager *cpa.Manager) *CPAHandler {
	return &CPAHandler{cfgRepo: cfgRepo, manager: manager}
}

// SetQuotaService 注入 Codex/其他 provider 额度服务。
func (h *CPAHandler) SetQuotaService(service *cpa.QuotaService) {
	h.quota = service
}

// clearLongRunningResponseDeadline 取消 API Server 的写超时。
// CLIProxyAPI 下载/解压可能超过主服务默认 WriteTimeout，必须在开始同步操作前清除。
func clearLongRunningResponseDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
}

// PrepareConfig 将数据库配置写入 CLIProxyAPI 配置文件。
func (h *CPAHandler) PrepareConfig() error {
	cfg, err := h.cfgRepo.Get()
	if err != nil {
		return err
	}
	if cfg.ManagementKey == "" {
		cfg.ManagementKey, err = h.cfgRepo.EnsureManagementKey()
		if err != nil {
			return err
		}
	}
	return cfgToCPAConfig(cfg).WriteConfig(cfg.DataDir)
}

// GetConfig 获取配置
func (h *CPAHandler) GetConfig(c *gin.Context) {
	cfg, err := h.cfgRepo.Get()
	if err != nil {
		// 首次使用，返回默认值
		_ = h.cfgRepo.Init(h.manager.BinPath())
		cfg, _ = h.cfgRepo.Get()
	}
	if cfg == nil {
		c.JSON(http.StatusOK, map[string]interface{}{"enabled": false})
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// SaveConfig 保存配置
func (h *CPAHandler) SaveConfig(c *gin.Context) {
	var cfg store.CPAConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	existing, err := h.cfgRepo.Get()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "配置不存在"})
		return
	}
	cfg.ID = existing.ID
	cfg.DataDir = existing.DataDir
	if err := h.cfgRepo.Save(&cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.manager.UpdateEndpoint(cfg.Host, cfg.Port)
	if h.quota != nil {
		h.quota.UpdateEndpoint(cfg.Host, cfg.Port)
	}
	// 写入 CLIProxyAPI config.yaml
	if err := h.PrepareConfig(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "写入 sidecar 配置失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// Status 获取 sidecar 状态
func (h *CPAHandler) Status(c *gin.Context) {
	c.JSON(http.StatusOK, h.manager.Status())
}

// Quota 获取已登录订阅账号的额度信息。
func (h *CPAHandler) Quota(c *gin.Context) {
	if h.quota == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "额度服务未初始化"})
		return
	}
	result, err := h.quota.Query(c.Request.Context(), c.Query("refresh") == "true")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

// Start 启动 sidecar
func (h *CPAHandler) Start(c *gin.Context) {
	// 先确保配置写入
	if err := h.PrepareConfig(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "写入配置失败: " + err.Error()})
		return
	}
	if err := h.manager.Start(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "started"})
}

// Stop 停止 sidecar
func (h *CPAHandler) Stop(c *gin.Context) {
	if err := h.manager.Stop(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

// Restart 重启 sidecar
func (h *CPAHandler) Restart(c *gin.Context) {
	if err := h.manager.Restart(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "restarted"})
}

// InstallBinary 安装/升级二进制
func (h *CPAHandler) InstallBinary(c *gin.Context) {
	// 下载/解压可能超过 API Server 的默认 WriteTimeout（60 秒），避免客户端收到“响应提前结束”，
	// 但后台实际已经完成安装的假失败。
	clearLongRunningResponseDeadline(c.Writer)

	// 从配置中读取出站代理，传给下载器
	if cfg, err := h.cfgRepo.Get(); err == nil {
		h.manager.SetProxyURL(cfg.ProxyURL)
	}
	version, err := h.manager.InstallBinary(c.Query("force") == "true")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": version, "status": "installed"})
}

// CheckUpdate 检查更新
func (h *CPAHandler) CheckUpdate(c *gin.Context) {
	clearLongRunningResponseDeadline(c.Writer)
	latest, hasUpdate, err := h.manager.CheckUpdate()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"latest_version": latest, "has_update": hasUpdate, "current_version": h.manager.BinVersion()})
}

// AuthStatus 获取 OAuth 登录和认证文件状态。
func (h *CPAHandler) AuthStatus(c *gin.Context) {
	c.JSON(http.StatusOK, h.manager.AuthStatus())
}

// StartAuth 启动指定订阅渠道的 OAuth 登录。
func (h *CPAHandler) StartAuth(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Provider == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider 为必填项"})
		return
	}
	if err := h.PrepareConfig(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "写入配置失败: " + err.Error()})
		return
	}
	if err := h.manager.StartLogin(req.Provider, req.Device, req.NoBrowser); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "started", "provider": req.Provider})
}

// StopAuth 取消当前 OAuth 登录。
func (h *CPAHandler) StopAuth(c *gin.Context) {
	if err := h.manager.StopLogin(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "stopped"})
}

// cfgToCPAConfig 转换 store.CPAConfig → cpa.Config
func cfgToCPAConfig(cfg *store.CPAConfig) *cpa.Config {
	return &cpa.Config{
		Enabled:       cfg.Enabled,
		AutoStart:     cfg.AutoStart,
		Host:          cfg.Host,
		Port:          cfg.Port,
		APIKeys:       cfg.APIKeys,
		ManagementKey: cfg.ManagementKey,
		ProxyURL:      cfg.ProxyURL,
		RequestRetry:  cfg.RequestRetry,
		Debug:         cfg.Debug,
	}
}
