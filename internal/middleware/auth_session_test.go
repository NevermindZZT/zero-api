package middleware

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
)

type authSessionMock struct {
	session                       *store.AuthSession
	renewed                       bool
	err                           error
	calls                         int
	gotToken, gotUser, gotVersion string
}

func (m *authSessionMock) Create(context.Context, string, string, string, time.Time) (string, *store.AuthSession, error) {
	panic("unexpected Create")
}
func (m *authSessionMock) Revoke(context.Context, string) error { panic("unexpected Revoke") }
func (m *authSessionMock) Authenticate(_ context.Context, token, user, version string, _ time.Time) (*store.AuthSession, bool, error) {
	m.calls++
	m.gotToken, m.gotUser, m.gotVersion = token, user, version
	return m.session, m.renewed, m.err
}

func middlewareAuthRouter(mock *authSessionMock, now time.Time, enabled bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(authMiddleware(enabled, mock, "admin", "account-version", func() time.Time { return now }), CORS())
	for _, path := range []string{"/api/private", "/api/auth/login", "/api/auth/logout", "/api/auth/session", "/v1/models", "/mcp", "/settings", "/assets/app.js"} {
		r.Any(path, func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	}
	return r
}

func TestAuthSessionMiddlewareCookieOnlyAndScope(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mock := &authSessionMock{session: &store.AuthSession{Username: "admin", ExpiresAt: now.Add(store.SessionTTL)}}
	r := middlewareAuthRouter(mock, now, true)
	for _, path := range []string{"/v1/models", "/mcp", "/settings", "/assets/app.js", "/api/auth/login", "/api/auth/logout"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Errorf("%s: status %d", path, w.Code)
		}
	}
	for _, auth := range []string{"", "Bearer old-signed-admin-token", "Basic YWRtaW46cGFzcw=="} {
		req := httptest.NewRequest("GET", "/api/private", nil)
		req.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 401 {
			t.Errorf("Authorization %q bypassed: %d", auth, w.Code)
		}
	}
	if mock.calls != 0 {
		t.Fatal("repository accessed without Cookie")
	}
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/private", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "opaque-cookie"})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 200 || len(w.Result().Cookies()) != 0 {
			t.Fatalf("unrenewed request: %d %v", w.Code, w.Result().Cookies())
		}
	}
	if mock.calls != 2 || mock.gotToken != "opaque-cookie" || mock.gotUser != "admin" || mock.gotVersion != "account-version" {
		t.Fatalf("every request must authenticate: %+v", mock)
	}
}

func TestAuthSessionMiddlewareRenewalAndDatabaseErrors(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name            string
		session         *store.AuthSession
		renewed         bool
		err             error
		status, cookies int
	}{
		{"renewed", &store.AuthSession{Username: "admin", ExpiresAt: now.Add(store.SessionTTL)}, true, nil, 200, 1},
		{"unrenewed", &store.AuthSession{Username: "admin", ExpiresAt: now.Add(store.SessionRenewBefore)}, false, nil, 200, 0},
		{"expired", nil, false, nil, 401, 1},
		{"database-error", nil, false, errors.New("database unavailable"), 503, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			mock := &authSessionMock{session: test.session, renewed: test.renewed, err: test.err}
			req := httptest.NewRequest("GET", "/api/private", nil)
			req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "opaque-cookie"})
			w := httptest.NewRecorder()
			middlewareAuthRouter(mock, now, true).ServeHTTP(w, req)
			cookies := w.Result().Cookies()
			if w.Code != test.status || len(cookies) != test.cookies {
				t.Fatalf("status=%d cookies=%v", w.Code, cookies)
			}
			if test.renewed && (cookies[0].MaxAge != int(store.SessionTTL/time.Second) || !cookies[0].Expires.Equal(test.session.ExpiresAt)) {
				t.Fatal("renewed Cookie did not use database expiry")
			}
			if test.name == "expired" && (cookies[0].Name != SessionCookieName || cookies[0].MaxAge != -1) {
				t.Fatal("invalid session must clear only its own Cookie")
			}
		})
	}
}

func TestAuthSessionCSRFBeforeAuthenticationIncludingGETAndPreflight(t *testing.T) {
	now := time.Now()
	mock := &authSessionMock{session: &store.AuthSession{Username: "admin", ExpiresAt: now.Add(store.SessionTTL)}, renewed: true}
	r := middlewareAuthRouter(mock, now, true)
	for _, test := range []struct {
		name, origin, site string
		status             int
	}{
		{"no-browser-headers", "", "", 200}, {"same-origin", "https://admin.example:8443", "same-origin", 200},
		{"scheme-http", "http://admin.example:8443", "none", 200}, {"case-host", "https://ADMIN.EXAMPLE:8443", "same-origin", 200},
		{"null", "null", "", 403}, {"other-host", "https://evil.example", "", 403},
		{"other-port", "https://admin.example", "", 403}, {"userinfo", "https://evil@admin.example:8443", "", 403},
		{"bad-scheme", "file://admin.example:8443", "", 403}, {"origin-path", "https://admin.example:8443/evil", "", 403},
		{"cross-site", "", "cross-site", 403}, {"same-site", "https://admin.example:8443", "same-site", 403},
	} {
		for _, method := range []string{"GET", "POST", "OPTIONS"} {
			t.Run(test.name+method, func(t *testing.T) {
				req := httptest.NewRequest(method, "http://admin.example:8443/api/private", nil)
				if test.origin != "" {
					req.Header.Set("Origin", test.origin)
				}
				if test.site != "" {
					req.Header.Set("Sec-Fetch-Site", test.site)
				}
				req.Header.Set("X-Forwarded-Host", "evil.example")
				req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "opaque-cookie"})
				before := mock.calls
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				expected := test.status
				if method == "OPTIONS" && expected == 200 {
					expected = 204
				}
				if w.Code != expected {
					t.Fatalf("status=%d want=%d", w.Code, expected)
				}
				if test.status == 403 && (mock.calls != before || len(w.Result().Cookies()) != 0) {
					t.Fatal("CSRF must not authenticate or renew")
				}
			})
		}
	}
	for _, path := range []string{"/api/auth/login", "/api/auth/logout"} {
		req := httptest.NewRequest("POST", path, nil)
		req.Header.Set("Origin", "null")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Errorf("public auth endpoint %s escaped CSRF", path)
		}
	}
}

func TestAuthSessionSQLiteExactRenewalBoundaryAndInactiveExpiry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewSessionRepo(db)
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	token, original, err := repo.Create(context.Background(), "admin", "version", "", createdAt)
	if err != nil {
		t.Fatal(err)
	}
	// Close and reopen the actual SQLite file to verify persistence across restarts.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	repo = store.NewSessionRepo(db)
	for _, test := range []struct {
		name    string
		now     time.Time
		cookies int
	}{
		{"exactly-15-days", createdAt.Add(15 * 24 * time.Hour), 0},
		{"less-than-15-days", createdAt.Add(15*24*time.Hour + time.Second), 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := gin.New()
			r.Use(authMiddleware(true, repo, "admin", "version", func() time.Time { return test.now }))
			r.GET("/api/private", func(c *gin.Context) { c.Status(200) })
			req := httptest.NewRequest("GET", "/api/private", nil)
			req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 200 || len(w.Result().Cookies()) != test.cookies {
				t.Fatalf("status=%d cookies=%d", w.Code, len(w.Result().Cookies()))
			}
			if test.cookies == 1 && !w.Result().Cookies()[0].Expires.Equal(test.now.Add(store.SessionTTL)) {
				t.Fatal("renewal did not persist 30-day expiry")
			}
		})
	}
	inactive, _, err := repo.Create(context.Background(), "admin", "version", "", createdAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, expiredAt := range []time.Time{original.ExpiresAt, original.ExpiresAt.Add(time.Hour)} {
		session, renewed, err := repo.Authenticate(context.Background(), inactive, "admin", "version", expiredAt)
		if err != nil || session != nil || renewed {
			t.Fatalf("inactive expired session revived: %+v %v %v", session, renewed, err)
		}
	}
}

func TestAuthSessionSafeRecoveryDoesNotLeakCredentials(t *testing.T) {
	var output bytes.Buffer
	oldOutput, oldGin := log.Writer(), gin.DefaultErrorWriter
	log.SetOutput(&output)
	gin.DefaultErrorWriter = &output
	t.Cleanup(func() { log.SetOutput(oldOutput); gin.DefaultErrorWriter = oldGin })
	secret := "synthetic-thirty-day-cookie-DO-NOT-LOG"
	for _, written := range []bool{false, true} {
		r := gin.New()
		r.Use(SafeRecovery())
		r.GET("/panic", func(c *gin.Context) {
			if written {
				c.String(200, "already-written")
			}
			panic("panic value includes " + secret)
		})
		req := httptest.NewRequest("GET", "/panic", nil)
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: secret})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if strings.Contains(output.String(), secret) || strings.Contains(w.Body.String(), secret) {
			t.Fatal("recovery leaked Cookie or panic value")
		}
		if !written && w.Code != 500 {
			t.Fatalf("panic status=%d", w.Code)
		}
		if written && (w.Code != 200 || w.Body.String() != "already-written") {
			t.Fatal("recovery appended to written response")
		}
	}
}

func TestAuthSessionStripCookiePreservesOtherCredentials(t *testing.T) {
	req := httptest.NewRequest("GET", "http://upstream.example/management", nil)
	req.Header.Add("Cookie", "other=one; zero_api_session=synthetic-secret")
	req.Header.Add("Cookie", "third=two; zero_api_session=another-secret")
	StripSessionCookie(req)
	if _, err := req.Cookie(SessionCookieName); err != http.ErrNoCookie {
		t.Fatal("admin credential forwarded upstream")
	}
	for name, expected := range map[string]string{"other": "one", "third": "two"} {
		cookie, err := req.Cookie(name)
		if err != nil || cookie.Value != expected {
			t.Fatalf("other Cookie %s lost", name)
		}
	}
	only := httptest.NewRequest("GET", "/", nil)
	only.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "synthetic-secret"})
	StripSessionCookie(only)
	if len(only.Header.Values("Cookie")) != 0 {
		t.Fatal("empty Cookie header retained")
	}
}

func TestAuthSessionCookieSecurityAndAccountVersion(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, test := range []struct {
		name   string
		tls    bool
		xfp    []string
		secure bool
	}{
		{"plain", false, nil, false}, {"tls", true, nil, true}, {"proxy-https", false, []string{"https"}, true},
		{"tls-no-downgrade", true, []string{"http"}, true}, {"ambiguous-list", false, []string{"https,http"}, false},
		{"ambiguous-headers", false, []string{"https", "https"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/auth/session", nil)
			if test.tls {
				req.TLS = &tls.ConnectionState{}
			}
			for _, value := range test.xfp {
				req.Header.Add("X-Forwarded-Proto", value)
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req
			SetSessionCookie(c, "opaque-cookie", now.Add(store.SessionTTL), now)
			cookie := w.Result().Cookies()[0]
			if cookie.Name != SessionCookieName || cookie.Path != "/api/" || cookie.Domain != "" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Secure != test.secure || cookie.MaxAge != 2592000 {
				t.Fatalf("bad Cookie attributes: %+v", cookie)
			}
		})
	}
	base := AccountVersion("admin", "password", "secret")
	for _, candidate := range []string{AccountVersion("other", "password", "secret"), AccountVersion("admin", "changed", "secret"), AccountVersion("admin", "password", "changed")} {
		if candidate == base {
			t.Fatal("credential change did not invalidate account version")
		}
	}
	if AccountVersion("ab", "c", "s") == AccountVersion("a", "bc", "s") {
		t.Fatal("ambiguous account-version encoding")
	}
}
