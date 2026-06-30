package upstream

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
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

// NewHTTPClientWithProxy 创建支持出站代理的 HTTP 客户端
// proxyURL 格式: http://host:port 或 http://user:pass@host:port
// proxyUser/proxyPass 可选，若提供则覆盖 URL 中的 userinfo
func NewHTTPClientWithProxy(proxyURL, proxyUser, proxyPass string) (*http.Client, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("代理 URL 解析失败: %w", err)
	}

	// 如果有独立的用户名密码，覆盖 URL 中的 userinfo
	if proxyUser != "" {
		u.User = url.UserPassword(proxyUser, proxyPass)
	}

	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(u),
			TLSClientConfig:     &tls.Config{},
			TLSHandshakeTimeout: 15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConns:        10,
			IdleConnTimeout:     90 * time.Second,
		},
	}, nil
}
