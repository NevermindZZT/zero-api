package upstream

import (
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const defaultResponseHeaderTimeout = 60 * time.Second

// defaultTransport 共享的 HTTP 传输层，所有非代理请求复用此连接池
var defaultTransport = &http.Transport{
	TLSClientConfig:       &tls.Config{},
	DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: defaultResponseHeaderTimeout,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	IdleConnTimeout:       90 * time.Second,
	DisableCompression:    false,
}

// Transport 缓存同时用于直连和代理；凭据只以摘要参与键，变更密码后不会复用旧凭据。
var (
	proxyTransportMu    sync.Mutex
	proxyTransportCache = make(map[string]*http.Transport)
)

func getProxyTransport(proxyURL, proxyUser, proxyPass string) (*http.Transport, error) {
	return getProxyTransportWithHeaderTimeout(proxyURL, proxyUser, proxyPass, defaultResponseHeaderTimeout)
}

func getProxyTransportWithHeaderTimeout(proxyURL, proxyUser, proxyPass string, responseHeaderTimeout time.Duration) (*http.Transport, error) {
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = defaultResponseHeaderTimeout
	}
	var u *url.URL
	if proxyURL != "" {
		var err error
		u, err = url.Parse(proxyURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
			return nil, fmt.Errorf("代理 URL 无效或协议不受支持")
		}
		if proxyUser != "" {
			u.User = url.UserPassword(proxyUser, proxyPass)
		}
	}
	digest := sha256.Sum256([]byte(proxyURL + "\x00" + proxyUser + "\x00" + proxyPass))
	cacheKey := fmt.Sprintf("%x|%d", digest, responseHeaderTimeout)
	proxyTransportMu.Lock()
	defer proxyTransportMu.Unlock()
	if t, ok := proxyTransportCache[cacheKey]; ok {
		return t, nil
	}
	t := defaultTransport.Clone()
	t.ResponseHeaderTimeout = responseHeaderTimeout
	if u != nil {
		t.Proxy = http.ProxyURL(u)
	}
	// 配置热更不能无限积累连接池；CloseIdleConnections 不影响正在运行的请求。
	if len(proxyTransportCache) >= 64 {
		for key, old := range proxyTransportCache {
			old.CloseIdleConnections()
			delete(proxyTransportCache, key)
			break
		}
	}
	proxyTransportCache[cacheKey] = t
	return t, nil
}

func directTransport(timeout time.Duration) *http.Transport {
	t, _ := getProxyTransportWithHeaderTimeout("", "", "", timeout)
	return t
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
		Transport: directTransport(timeout),
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
		Timeout:   0,
		Transport: directTransport(timeout),
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
	t, err := getProxyTransportWithHeaderTimeout(proxyURL, proxyUser, proxyPass, timeout)
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
