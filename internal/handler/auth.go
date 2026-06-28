package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/middleware"
)

type AuthHandler struct {
	username string
	password string
	secret   string
}

func NewAuthHandler(username, password, secret string) *AuthHandler {
	return &AuthHandler{username: username, password: password, secret: secret}
}

// Login 登录认证
func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}

	if req.Username != h.username || req.Password != h.password {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	token, err := middleware.GenerateToken(req.Username, h.secret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成 token 失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":   token,
		"user":    req.Username,
		"expires": "24h",
	})
}
