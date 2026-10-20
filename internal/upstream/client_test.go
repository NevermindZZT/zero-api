package upstream

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewStreamHTTPClientWithTimeoutControlsResponseHeaderTimeout(t *testing.T) {
	const responseDelay = 100 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(responseDelay)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewStreamHTTPClientWithTimeout(25 * time.Millisecond)
	if client.Timeout != 0 {
		t.Fatalf("stream client total timeout = %v, want no total timeout", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("stream client transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.ResponseHeaderTimeout != 25*time.Millisecond {
		t.Fatalf("response header timeout = %v, want 25ms", transport.ResponseHeaderTimeout)
	}
	if _, err := client.Get(server.URL); err == nil {
		t.Fatal("expected delayed response headers to exceed 25ms timeout")
	}

	client = NewStreamHTTPClientWithTimeout(500 * time.Millisecond)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("request with 500ms response header timeout failed: %v", err)
	}
	_ = resp.Body.Close()
}

func TestNewStreamHTTPClientWithProxyCachesByHeaderTimeout(t *testing.T) {
	proxyURL := "http://stream-timeout-cache-test.invalid:3128"
	shortTimeout := 41 * time.Second
	longTimeout := 127 * time.Second

	shortClient, err := NewStreamHTTPClientWithProxyAndTimeout(proxyURL, "", "", shortTimeout)
	if err != nil {
		t.Fatalf("create short-timeout proxy client: %v", err)
	}
	longClient, err := NewStreamHTTPClientWithProxyAndTimeout(proxyURL, "", "", longTimeout)
	if err != nil {
		t.Fatalf("create long-timeout proxy client: %v", err)
	}
	shortClientAgain, err := NewStreamHTTPClientWithProxyAndTimeout(proxyURL, "", "", shortTimeout)
	if err != nil {
		t.Fatalf("recreate short-timeout proxy client: %v", err)
	}

	shortTransport := shortClient.Transport.(*http.Transport)
	longTransport := longClient.Transport.(*http.Transport)
	shortTransportAgain := shortClientAgain.Transport.(*http.Transport)
	if shortTransport.ResponseHeaderTimeout != shortTimeout {
		t.Fatalf("short proxy response header timeout = %v, want %v", shortTransport.ResponseHeaderTimeout, shortTimeout)
	}
	if longTransport.ResponseHeaderTimeout != longTimeout {
		t.Fatalf("long proxy response header timeout = %v, want %v", longTransport.ResponseHeaderTimeout, longTimeout)
	}
	if shortTransport == longTransport {
		t.Fatal("proxy transports with different response header timeouts must not share a cache entry")
	}
	if shortTransport != shortTransportAgain {
		t.Fatal("proxy transports with identical configuration should reuse the cached transport")
	}
	if shortClient.Timeout != 0 || longClient.Timeout != 0 {
		t.Fatal("stream proxy clients must not impose a total request timeout")
	}
}

func TestStreamHTTPClientNonPositiveTimeoutUsesDefault(t *testing.T) {
	client := NewStreamHTTPClientWithTimeout(0)
	transport := client.Transport.(*http.Transport)
	if transport.ResponseHeaderTimeout != defaultResponseHeaderTimeout {
		t.Fatalf("response header timeout = %v, want default %v", transport.ResponseHeaderTimeout, defaultResponseHeaderTimeout)
	}
}
