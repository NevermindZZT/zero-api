package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
)

const (
	SessionCookieName     = "zero_api_session"
	SessionCookiePath     = "/api/"
	authSessionContextKey = "zero_api_auth_session"
)

// SessionRepository keeps raw browser credentials out of API responses and storage.
// The store implementation hashes tokens and atomically renews/revokes sessions.
type SessionRepository interface {
	Create(context.Context, string, string, string, time.Time) (string, *store.AuthSession, error)
	Authenticate(context.Context, string, string, string, time.Time) (*store.AuthSession, bool, error)
	Revoke(context.Context, string) error
}

// AccountVersion invalidates persisted sessions whenever any login credential changes.
// JSON encoding avoids ambiguous concatenations of usernames and passwords.
func AccountVersion(username, password, secret string) string {
	data, _ := json.Marshal([]string{"zero-api-admin-session-v1", username, password})
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// CheckAdminOrigin rejects cross-origin browser access even for GETs, since reads
// can renew sessions. Only the request Host is authoritative (never Forwarded-Host).
func CheckAdminOrigin(c *gin.Context) bool {
	request := c.Request
	fetchSites := request.Header.Values("Sec-Fetch-Site")
	if len(fetchSites) > 1 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "不允许跨站管理请求"})
		return false
	}
	if len(fetchSites) == 1 {
		switch strings.ToLower(strings.TrimSpace(fetchSites[0])) {
		case "same-origin", "none", "":
		default:
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "不允许跨站管理请求"})
			return false
		}
	}
	origins := request.Header.Values("Origin")
	if len(origins) == 0 {
		return true
	}
	if len(origins) == 1 {
		origin, err := url.Parse(origins[0])
		if err == nil && (origin.Scheme == "http" || origin.Scheme == "https") &&
			origin.Host != "" && origin.User == nil && origin.Opaque == "" &&
			origin.Path == "" && origin.RawQuery == "" && origin.Fragment == "" &&
			strings.EqualFold(origin.Host, request.Host) {
			return true
		}
	}
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "不允许跨源管理请求"})
	return false
}

// SessionCookieSecure never lets a forwarded HTTP value downgrade actual TLS.
// A reverse proxy must overwrite X-Forwarded-Proto and preserve the original Host.
func SessionCookieSecure(request *http.Request) bool {
	if request.TLS != nil {
		return true
	}
	values := request.Header.Values("X-Forwarded-Proto")
	return len(values) == 1 && strings.EqualFold(strings.TrimSpace(values[0]), "https")
}

// SetSessionCookie uses the database expiry, not a fresh client-side 30-day clock.
func SetSessionCookie(c *gin.Context, rawToken string, expiresAt, now time.Time) {
	maxAge := int(expiresAt.Sub(now) / time.Second)
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: SessionCookieName, Value: rawToken, Path: SessionCookiePath,
		Expires: expiresAt.UTC(), MaxAge: maxAge, HttpOnly: true,
		Secure: SessionCookieSecure(c.Request), SameSite: http.SameSiteStrictMode,
	})
}

func ClearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: SessionCookieName, Value: "", Path: SessionCookiePath,
		Expires: time.Unix(1, 0).UTC(), MaxAge: -1, HttpOnly: true,
		Secure: SessionCookieSecure(c.Request), SameSite: http.SameSiteStrictMode,
	})
}

func AuthSessionFromContext(c *gin.Context) *store.AuthSession {
	value, exists := c.Get(authSessionContextKey)
	if !exists {
		return nil
	}
	session, _ := value.(*store.AuthSession)
	return session
}

// AuthMiddleware protects only the admin API. /v1, /mcp and SPA routes retain
// their own authentication. Legacy Authorization Bearer tokens are not accepted.
func AuthMiddleware(enabled bool, sessions SessionRepository, username, accountVersion string) gin.HandlerFunc {
	return authMiddleware(enabled, sessions, username, accountVersion, time.Now)
}

func authMiddleware(enabled bool, sessions SessionRepository, username, accountVersion string, now func() time.Time) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/api/") {
			c.Next()
			return
		}
		c.Header("Cache-Control", "no-store")
		if !CheckAdminOrigin(c) {
			return
		}
		// The CORS middleware may answer an allowed preflight after this check.
		if c.Request.Method == http.MethodOptions || path == "/api/auth/login" || path == "/api/auth/logout" || !enabled {
			c.Next()
			return
		}
		rawToken, err := c.Cookie(SessionCookieName)
		if err != nil || rawToken == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
			return
		}
		if sessions == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "会话服务暂不可用"})
			return
		}
		checkedAt := now().UTC().Truncate(time.Second)
		session, renewed, err := sessions.Authenticate(c.Request.Context(), rawToken, username, accountVersion, checkedAt)
		if err != nil {
			// A transient database error must not destroy a still-valid browser cookie.
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "会话服务暂不可用"})
			return
		}
		if session == nil {
			ClearSessionCookie(c)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "会话已失效，请重新登录"})
			return
		}
		c.Set("auth_user", session.Username)
		c.Set(authSessionContextKey, session)
		if renewed && path != "/api/auth/session" {
			SetSessionCookie(c, rawToken, session.ExpiresAt, checkedAt)
		}
		c.Next()
	}
}

// SafeRecovery avoids Gin's default request-header dump, which includes the
// long-lived session Cookie. Never log a panic value: it may contain credentials.
func SafeRecovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, _ any) {
		log.Print("[http] recovered request panic; sensitive details omitted")
		if !c.Writer.Written() {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "内部服务错误"})
			return
		}
		c.Abort()
	})
}

// StripSessionCookie terminates the admin credential at zero-api. Other cookies
// remain available to the upstream native management panel.
func StripSessionCookie(request *http.Request) {
	cookies := request.Cookies()
	found := false
	kept := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == SessionCookieName {
			found = true
			continue
		}
		kept = append(kept, (&http.Cookie{Name: cookie.Name, Value: cookie.Value}).String())
	}
	if !found {
		return
	}
	request.Header.Del("Cookie")
	if len(kept) > 0 {
		request.Header.Set("Cookie", strings.Join(kept, "; "))
	}
}

// CheckProxyBasicAuth 验证代理 Basic 认证（MITM 独立于管理后台 Cookie）。
func CheckProxyBasicAuth(authHeader, expectedUser, expectedPass string) bool {
	if authHeader == "" {
		return false
	}
	const prefix = "Basic "
	if !strings.HasPrefix(authHeader, prefix) {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(authHeader[len(prefix):])
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	return len(parts) == 2 && parts[0] == expectedUser && parts[1] == expectedPass
}
