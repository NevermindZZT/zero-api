package upstream

import (
	"net/http"
	"testing"
	"time"
)

func TestConfiguredNonStreamTimeoutAndDirectPoolReuse(t *testing.T) {
	timeout := 150 * time.Second
	a := NewHTTPClientWithTimeout(timeout)
	b := NewStreamHTTPClientWithTimeout(timeout)
	if a.Timeout != timeout || b.Timeout != 0 {
		t.Fatal("incorrect total timeout")
	}
	transport := a.Transport.(*http.Transport)
	if transport.ResponseHeaderTimeout != timeout {
		t.Fatalf("header timeout=%v", transport.ResponseHeaderTimeout)
	}
	if a.Transport != b.Transport {
		t.Fatal("equivalent direct clients should share a connection pool")
	}
	if transport.DialContext == nil {
		t.Fatal("dial phase must have a finite timeout")
	}
}

func TestProxyPasswordChangesDoNotReuseOldCredentials(t *testing.T) {
	a, err := NewHTTPClientWithProxyAndTimeout("http://proxy.invalid:3128", "tester", "old-pass", 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewHTTPClientWithProxyAndTimeout("http://proxy.invalid:3128", "tester", "new-pass", 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if a.Transport == b.Transport {
		t.Fatal("password update reused old proxy transport")
	}
	tr := b.Transport.(*http.Transport)
	u, err := tr.Proxy(&http.Request{})
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	if password != "new-pass" {
		t.Fatal("proxy password was not updated")
	}
	if tr.ResponseHeaderTimeout != 2*time.Minute {
		t.Fatal("nonstream proxy header timeout not honored")
	}
}

func TestInvalidProxyDoesNotLeakInput(t *testing.T) {
	for _, proxy := range []string{"not-a-url", "file:///sensitive", "http://"} {
		if _, err := NewHTTPClientWithProxy(proxy, "", ""); err == nil {
			t.Fatalf("accepted invalid proxy")
		}
	}
}
