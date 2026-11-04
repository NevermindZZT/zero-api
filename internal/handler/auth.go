package handler

import (
	"crypto/subtle"
	"mime"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/middleware"
	"github.com/never/zero-api/internal/store"
)

type AuthHandler struct {
	username       string
	password       string
	accountVersion string
	sessions       middleware.SessionRepository
	enabled        bool
	now            func() time.Time
}

func NewAuthHandler(username, password, secret string, sessions middleware.SessionRepository, enabled bool) *AuthHandler {
	return &AuthHandler{
		username: username, password: password,
		accountVersion: middleware.AccountVersion(username, password, secret),
		sessions:       sessions, enabled: enabled, now: time.Now,
	}
}

// Login creates an opaque database session. Its credential is only sent as an
// HttpOnly cookie, never in JSON, logs or URLs.
func (h *AuthHandler) Login(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !middleware.CheckAdminOrigin(c) {
		return
	}
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "登录请求必须使用 application/json"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误"})
		return
	}
	if !h.enabled {
		h.respondDisabled(c)
		return
	}
	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(h.username))
	passwordOK := subtle.ConstantTimeCompare([]byte(req.Password), []byte(h.password))
	if userOK&passwordOK != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	if h.sessions == nil {
		h.unavailable(c)
		return
	}
	previousToken, _ := c.Cookie(middleware.SessionCookieName)
	now := h.now().UTC().Truncate(time.Second)
	rawToken, session, err := h.sessions.Create(c.Request.Context(), h.username, h.accountVersion, previousToken, now)
	if err != nil || session == nil || rawToken == "" {
		h.unavailable(c)
		return
	}
	middleware.SetSessionCookie(c, rawToken, session.ExpiresAt, now)
	h.respondSession(c, session, now)
}

// Session resends the current database expiry to heal a lost renewal response.
// It must not blindly extend a cookie when no database renewal occurred.
func (h *AuthHandler) Session(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !middleware.CheckAdminOrigin(c) {
		return
	}
	if !h.enabled {
		h.respondDisabled(c)
		return
	}
	session := middleware.AuthSessionFromContext(c)
	rawToken, err := c.Cookie(middleware.SessionCookieName)
	if session == nil || err != nil || rawToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	now := h.now().UTC().Truncate(time.Second)
	// The middleware leaves this endpoint's cookie refresh to the handler.
	middleware.SetSessionCookie(c, rawToken, session.ExpiresAt, now)
	h.respondSession(c, session, now)
}

// Logout is public but same-origin only, and succeeds only after server-side
// revocation. Missing and expired cookies are deliberately idempotent.
func (h *AuthHandler) Logout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !middleware.CheckAdminOrigin(c) {
		return
	}
	rawToken, _ := c.Cookie(middleware.SessionCookieName)
	if rawToken != "" {
		if h.sessions == nil {
			h.unavailable(c)
			return
		}
		if err := h.sessions.Revoke(c.Request.Context(), rawToken); err != nil {
			h.unavailable(c)
			return
		}
	}
	middleware.ClearSessionCookie(c)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AuthHandler) respondDisabled(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"authenticated": true, "auth_enabled": false, "user": h.username})
}

func (h *AuthHandler) respondSession(c *gin.Context, session *store.AuthSession, now time.Time) {
	seconds := int64(session.ExpiresAt.Sub(now) / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	c.JSON(http.StatusOK, gin.H{
		"authenticated": true, "auth_enabled": true, "user": session.Username,
		"expires_at": session.ExpiresAt.UTC().Format(time.RFC3339), "expires_in": seconds,
	})
}

func (h *AuthHandler) unavailable(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "会话服务暂不可用"})
}
