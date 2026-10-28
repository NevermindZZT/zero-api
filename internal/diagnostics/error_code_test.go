package diagnostics

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLoggerAllowsOnlyFixedUpstreamErrorCodes(t *testing.T) {
	opts := logOptions(t)
	l, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	allowed := []string{"context_length_exceeded", "rate_limit_exceeded", "insufficient_quota", "invalid_api_key", "authentication_error", "permission_denied", "previous_response_not_found", "tool_call_mismatch", "invalid_request", "server_error", "unknown"}
	for _, code := range allowed {
		e := auditEvent()
		e.UpstreamErrorCode = code
		l.Record(e)
	}
	// Use normalization independently of rotation to assert every allowed value.
	for _, code := range allowed {
		e := normalize(Event{UpstreamErrorCode: code})
		if e.UpstreamErrorCode != code {
			t.Errorf("allowed code dropped: %s", code)
		}
	}
	for _, code := range []string{"SECRET_ERROR_MESSAGE", "sk-SECRET", "context_length_exceeded SECRET", "https://secret.invalid", "unknown\nSECRET"} {
		e := auditEvent()
		e.UpstreamErrorCode = code
		l.Record(e)
		if normalize(e).UpstreamErrorCode != "" {
			t.Fatal("untrusted code retained")
		}
	}
	b, err := os.ReadFile(opts.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SECRET") {
		t.Fatal("upstream error content leaked")
	}
}
func TestRequestMiddlewareLogsPanicWithEitherRecoveryOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, outerRecovery := range []bool{true, false} {
		rec := &capturedRecorder{}
		r := gin.New()
		// The test recovery deliberately does not print the panic value.
		recovery := func(c *gin.Context) {
			defer func() {
				if recover() != nil {
					c.AbortWithStatus(500)
				}
			}()
			c.Next()
		}
		if outerRecovery {
			r.Use(recovery, RequestMiddleware(rec))
		} else {
			r.Use(RequestMiddleware(rec), recovery)
		}
		r.GET("/v1/models", func(c *gin.Context) { panic("SECRET_PANIC_CONTENT") })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/models", nil))
		if w.Code != 500 || len(rec.events) != 1 || rec.events[0].Status != 500 || rec.events[0].Category != "internal" {
			t.Fatalf("panic lost outer=%t code=%d events=%+v", outerRecovery, w.Code, rec.events)
		}
		b, _ := json.Marshal(rec.events)
		if strings.Contains(string(b), "SECRET") {
			t.Fatal("panic content leaked")
		}
	}
}
