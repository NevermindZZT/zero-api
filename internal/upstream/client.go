package upstream

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const defaultResponseHeaderTimeout = 60 * time.Second

// defaultTransport 共享的 HTTP 传输层，所有非代理请求复用此连接池
var defaultTransport = &http.Transport{
	TLSClientConfig:       &tls.Config{},
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: defaultResponseHeaderTimeout,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	IdleConnTimeout:       90 * time.Second,
	DisableCompression:    false,
}

// proxyTransportCache 代理 Transport 缓存，避免每次请求新建 Transport
var (
	proxyTransportMu    sync.RWMutex
	proxyTransportCache = make(map[string]*http.Transport)
)

func getProxyTransport(proxyURL, proxyUser, proxyPass string) (*http.Transport, error) {
	return getProxyTransportWithHeaderTimeout(proxyURL, proxyUser, proxyPass, defaultResponseHeaderTimeout)
}

func getProxyTransportWithHeaderTimeout(proxyURL, proxyUser, proxyPass string, responseHeaderTimeout time.Duration) (*http.Transport, error) {
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = defaultResponseHeaderTimeout
	}
	// 响应头超时是 Transport 的固定属性，必须纳入缓存键，避免不同请求设置互相覆盖。
	cacheKey := fmt.Sprintf("%s|%s|%s", proxyURL, proxyUser, responseHeaderTimeout)
	proxyTransportMu.RLock()
	if t, ok := proxyTransportCache[cacheKey]; ok {
		proxyTransportMu.RUnlock()
		return t, nil
	}
	proxyTransportMu.RUnlock()

	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("代理 URL 解析失败: %w", err)
	}
	if proxyUser != "" {
		u.User = url.UserPassword(proxyUser, proxyPass)
	}

	t := &http.Transport{
		Proxy:                 http.ProxyURL(u),
		TLSClientConfig:       &tls.Config{},
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
	}

	proxyTransportMu.Lock()
	proxyTransportCache[cacheKey] = t
	proxyTransportMu.Unlock()
	return t, nil
}

// NewHTTPClient 创建兼容性更好的 HTTP 客户端（非代理）
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: defaultTransport,
	}
}

// NewHTTPClientWithTimeout 创建带有指定超时的 HTTP 客户端（非代理）
func NewHTTPClientWithTimeout(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: defaultTransport,
	}
}

// NewStreamHTTPClient 创建使用默认响应头超时的流式客户端（无总超时）。
func NewStreamHTTPClient() *http.Client {
	return NewStreamHTTPClientWithTimeout(defaultResponseHeaderTimeout)
}

// NewStreamHTTPClientWithTimeout 创建流式专用 HTTP 客户端。
// timeout 只限制等待上游响应头的时间；流的总时长仍不受 http.Client.Timeout 限制。
func NewStreamHTTPClientWithTimeout(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultResponseHeaderTimeout
	}
	return &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			TLSClientConfig:       &tls.Config{},
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       120 * time.Second,
			DisableCompression:    false,
		},
	}
}

// NewStreamHTTPClientWithProxy 创建使用默认响应头超时的流式代理客户端。
func NewStreamHTTPClientWithProxy(proxyURL, proxyUser, proxyPass string) (*http.Client, error) {
	return NewStreamHTTPClientWithProxyAndTimeout(proxyURL, proxyUser, proxyPass, defaultResponseHeaderTimeout)
}

// NewStreamHTTPClientWithProxyAndTimeout 创建响应头超时可配置的流式代理客户端。
func NewStreamHTTPClientWithProxyAndTimeout(proxyURL, proxyUser, proxyPass string, timeout time.Duration) (*http.Client, error) {
	t, err := getProxyTransportWithHeaderTimeout(proxyURL, proxyUser, proxyPass, timeout)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout:   0,
		Transport: t,
	}, nil
}

// NewHTTPClientWithProxy 创建支持出站代理的 HTTP 客户端
func NewHTTPClientWithProxy(proxyURL, proxyUser, proxyPass string) (*http.Client, error) {
	t, err := getProxyTransport(proxyURL, proxyUser, proxyPass)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: t,
	}, nil
}

// NewHTTPClientWithProxyAndTimeout 创建支持代理且带超时的 HTTP 客户端
func NewHTTPClientWithProxyAndTimeout(proxyURL, proxyUser, proxyPass string, timeout time.Duration) (*http.Client, error) {
	t, err := getProxyTransport(proxyURL, proxyUser, proxyPass)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: t,
	}, nil
}

// ShouldFailoverStatus 判断 HTTP 状态码是否应触发渠道切换
func ShouldFailoverStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return statusCode >= 500
}
