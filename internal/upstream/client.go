package upstream

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// defaultTransport 共享的 HTTP 传输层，所有非代理请求复用此连接池
// ResponseHeaderTimeout 设 60s 以兼容慢速 LLM 上游
var defaultTransport = &http.Transport{
	TLSClientConfig:       &tls.Config{},
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 60 * time.Second,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	IdleConnTimeout:       90 * time.Second,
	DisableCompression:    false,
}

// NewHTTPClient 创建兼容性更好的 HTTP 客户端（非代理）
// 使用共享 defaultTransport，支持连接复用
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: defaultTransport,
	}
}

// NewHTTPClientWithTimeout 创建带有指定超时的 HTTP 客户端（非代理）
// 复用 defaultTransport 的连接池
func NewHTTPClientWithTimeout(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: defaultTransport,
	}
}

// NewHTTPClientWithProxy 创建支持出站代理的 HTTP 客户端
// 注意：为保持连接复用，transport 会被缓存，代理地址变化时需重新创建
func NewHTTPClientWithProxy(proxyURL, proxyUser, proxyPass string) (*http.Client, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("代理 URL 解析失败: %w", err)
	}
	if proxyUser != "" {
		u.User = url.UserPassword(proxyUser, proxyPass)
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyURL(u),
			TLSClientConfig:      &tls.Config{},
			TLSHandshakeTimeout:  15 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:  10,
			IdleConnTimeout:      90 * time.Second,
		},
	}, nil
}

// NewHTTPClientWithProxyAndTimeout 创建支持代理且带超时的 HTTP 客户端
func NewHTTPClientWithProxyAndTimeout(proxyURL, proxyUser, proxyPass string, timeout time.Duration) (*http.Client, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("代理 URL 解析失败: %w", err)
	}
	if proxyUser != "" {
		u.User = url.UserPassword(proxyUser, proxyPass)
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyURL(u),
			TLSClientConfig:      &tls.Config{},
			TLSHandshakeTimeout:  15 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:  10,
			IdleConnTimeout:      90 * time.Second,
		},
	}, nil
}
