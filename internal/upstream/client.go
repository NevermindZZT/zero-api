package upstream

import (
	"crypto/tls"
	"net/http"
	"time"
)

// NewHTTPClient 创建兼容性更好的 HTTP 客户端
// Go 默认 Transport 的 TLS 配置对某些服务端（如 Cloudflare）握手失败
// 显式设置 &tls.Config{} 可解决此问题
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{},
			TLSHandshakeTimeout: 15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConns:        10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}
