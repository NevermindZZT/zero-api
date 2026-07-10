package handler

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/config"
	"github.com/never/zero-api/internal/store"
)

type MCPConfigHandler struct {
	mcpCfg  config.MCPConfig
	proxyRepo *store.ProxyConfigRepo
}

func NewMCPConfigHandler(mcpCfg config.MCPConfig, proxyRepo *store.ProxyConfigRepo) *MCPConfigHandler {
	return &MCPConfigHandler{mcpCfg: mcpCfg, proxyRepo: proxyRepo}
}

// GetMCPStatus 返回 MCP 服务状态和配置
// GET /api/mcp/status
func (h *MCPConfigHandler) GetMCPStatus(c *gin.Context) {
	// 从 DB 读取 github_token
	githubToken := ""
	if pc, err := h.proxyRepo.Get(); err == nil {
		githubToken = pc.GitHubToken
	}

	mcpHost := c.Request.Host

	c.JSON(http.StatusOK, gin.H{
		"enabled":      h.mcpCfg.Enabled,
		"host":         mcpHost,
		"port":         "主 API 端口",
		"path":         "/mcp",
		"has_token":    h.mcpCfg.Token != "",
		"token":        h.mcpCfg.Token,
		"skills_dir":   h.mcpCfg.SkillsDir,
		"github_token": githubToken,
	})
}

// UpdateGitHubToken 更新 GitHub Token
// PUT /api/mcp/github-token
func (h *MCPConfigHandler) UpdateGitHubToken(c *gin.Context) {
	var req struct {
		Token string `json:"github_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求参数"})
		return
	}

	pc, err := h.proxyRepo.Get()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取配置失败"})
		return
	}

	pc.GitHubToken = req.Token
	if err := h.proxyRepo.Update(pc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "保存配置失败"})
		return
	}

	log.Printf("[MCP] GitHub Token 已更新")
	c.JSON(http.StatusOK, gin.H{"message": "GitHub Token 已保存"})
}
