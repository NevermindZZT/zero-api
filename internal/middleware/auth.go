package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware 创建认证中间件
func AuthMiddleware(enabled bool, secret string) gin.HandlerFunc {
	if !enabled {
		return func(c *gin.Context) {
			c.Next()
		}
	}

	if secret == "" {
		secret = "zero-api-default-secret"
	}

	return func(c *gin.Context) {
		// 白名单：非 /api/ 路径全部放行（前端 SPA 路由、静态资源、代理等）
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/api/") || path == "/api/auth/login" {
			c.Next()
			return
		}

		auth := c.GetHeader("Authorization")
		if auth == "" {
			abortUnauthorized(c, "缺少 Authorization 头")
			return
		}

		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			abortUnauthorized(c, "Authorization 格式错误，需 Bearer <token>")
			return
		}

		token := parts[1]
		user, ok := validateToken(token, secret)
		if !ok {
			abortUnauthorized(c, "无效或过期的 token")
			return
		}

		c.Set("auth_user", user)
		c.Next()
	}
}

// GenerateToken 生成认证 token
func GenerateToken(username, secret string) (string, error) {
	payload := map[string]interface{}{
		"user": username,
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
	}
	data, _ := json.Marshal(payload)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(data)
	sig := hex.EncodeToString(mac.Sum(nil))

	token := hex.EncodeToString(data) + "." + sig
	return token, nil
}

func validateToken(token, secret string) (string, bool) {
	dot := strings.LastIndex(token, ".")
	if dot < 0 {
		return "", false
	}

	dataHex := token[:dot]
	sigHex := token[dot+1:]

	data, err := hex.DecodeString(dataHex)
	if err != nil {
		return "", false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(data)
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sigHex), []byte(expectedSig)) {
		return "", false
	}

	var payload struct {
		User string `json:"user"`
		Exp  int64  `json:"exp"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", false
	}

	if time.Now().Unix() > payload.Exp {
		return "", false
	}

	return payload.User, true
}

func abortUnauthorized(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": msg})
}

// CheckProxyBasicAuth 验证代理 Basic 认证
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
	if len(parts) != 2 {
		return false
	}

	return parts[0] == expectedUser && parts[1] == expectedPass
}
