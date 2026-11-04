package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/middleware"
	"github.com/never/zero-api/internal/store"
)

type failingAuthSessions struct {
	middleware.SessionRepository
	createErr, authenticateErr, revokeErr bool
}

func (r *failingAuthSessions) Create(ctx context.Context, user, version, previous string, now time.Time) (string, *store.AuthSession, error) {
	if r.createErr {
		return "", nil, errors.New("private database error must not leak")
	}
	return r.SessionRepository.Create(ctx, user, version, previous, now)
}
func (r *failingAuthSessions) Authenticate(ctx context.Context, token, user, version string, now time.Time) (*store.AuthSession, bool, error) {
	if r.authenticateErr {
		return nil, false, errors.New("private database error must not leak")
	}
	return r.SessionRepository.Authenticate(ctx, token, user, version, now)
}
func (r *failingAuthSessions) Revoke(ctx context.Context, token string) error {
	if r.revokeErr {
		return errors.New("private database error must not leak")
	}
	return r.SessionRepository.Revoke(ctx, token)
}

func newAuthSessionTestRepo(t *testing.T) *store.SessionRepo {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "auth-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store.NewSessionRepo(db)
}
func authSessionTestRouter(repo middleware.SessionRepository, enabled bool, password string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewAuthHandler("admin", password, "test-only-secret", repo, enabled)
	r := gin.New()
	r.Use(middleware.AuthMiddleware(enabled, repo, "admin", middleware.AccountVersion("admin", password, "test-only-secret")), middleware.CORS())
	r.POST("/api/auth/login", h.Login)
	r.GET("/api/auth/session", h.Session)
	r.POST("/api/auth/logout", h.Logout)
	r.GET("/api/private", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	return r
}
func authSessionRequest(r http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
func authSessionLogin(t *testing.T, r http.Handler, cookie *http.Cookie) (*http.Cookie, map[string]interface{}) {
	t.Helper()
	w := authSessionRequest(r, "POST", "/api/auth/login", `{"username":"admin","password":"test-password"}`, cookie)
	if w.Code != 200 {
		t.Fatalf("login status=%d body=%s", w.Code, w.Body.String())
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected exactly one Cookie, got %d", len(cookies))
	}
	if strings.Contains(w.Body.String(), cookies[0].Value) || payload["token"] != nil || payload["expires"] != nil {
		t.Fatal("session credential or legacy token leaked in JSON")
	}
	return cookies[0], payload
}

func TestAuthSessionLoginThirtyDaysRotationAndLogout(t *testing.T) {
	repo := newAuthSessionTestRepo(t)
	r := authSessionTestRouter(repo, true, "test-password")
	before := time.Now().UTC().Truncate(time.Second)
	cookie, payload := authSessionLogin(t, r, nil)
	if payload["authenticated"] != true || payload["auth_enabled"] != true || payload["user"] != "admin" || payload["expires_in"] != float64(2592000) {
		t.Fatalf("unexpected login payload: %v", payload)
	}
	expiresAt, err := time.Parse(time.RFC3339, payload["expires_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if cookie.MaxAge != 2592000 || cookie.Expires.Before(before.Add(store.SessionTTL)) || !cookie.Expires.Equal(expiresAt) || cookie.Path != "/api/" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" {
		t.Fatalf("unexpected login Cookie: %+v", cookie)
	}
	if w := authSessionRequest(r, "GET", "/api/private", "", cookie); w.Code != 200 {
		t.Fatalf("logged-in API status=%d", w.Code)
	}
	secondCookie, _ := authSessionLogin(t, r, cookie)
	if secondCookie.Value == cookie.Value {
		t.Fatal("login did not rotate opaque token")
	}
	if w := authSessionRequest(r, "GET", "/api/private", "", cookie); w.Code != 401 {
		t.Fatalf("old browser session survived rotation: %d", w.Code)
	}
	// Constructing a fresh handler simulates an application restart with the same DB.
	r = authSessionTestRouter(repo, true, "test-password")
	w := authSessionRequest(r, "GET", "/api/auth/session", "", secondCookie)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("persisted session status=%d", w.Code)
	}
	if !w.Result().Cookies()[0].Expires.Equal(secondCookie.Expires) {
		t.Fatal("session inspection blindly extended cookie")
	}
	w = authSessionRequest(r, "POST", "/api/auth/logout", "", secondCookie)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"ok":true}` || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("logout status=%d", w.Code)
	}
	if w := authSessionRequest(r, "GET", "/api/private", "", secondCookie); w.Code != 401 {
		t.Fatal("logout failed to revoke database session")
	}
	for _, old := range []*http.Cookie{nil, secondCookie} {
		if w := authSessionRequest(r, "POST", "/api/auth/logout", "", old); w.Code != 200 {
			t.Fatalf("logout not idempotent: %d", w.Code)
		}
	}
}

func TestAuthSessionSelfHealsRenewalCookieAndInvalidatesPassword(t *testing.T) {
	repo := newAuthSessionTestRepo(t)
	now := time.Now().UTC().Truncate(time.Second)
	version := middleware.AccountVersion("admin", "test-password", "test-only-secret")
	token, old, err := repo.Create(context.Background(), "admin", version, "", now.Add(-16*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: middleware.SessionCookieName, Value: token}
	// Emulate a successful DB renewal whose HTTP response never reached the browser.
	current, renewed, err := repo.Authenticate(context.Background(), token, "admin", version, now)
	if err != nil || !renewed || current.ExpiresAt.Equal(old.ExpiresAt) {
		t.Fatalf("renewal fixture: %v %v", renewed, err)
	}
	r := authSessionTestRouter(repo, true, "test-password")
	w := authSessionRequest(r, "GET", "/api/auth/session", "", cookie)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 || !w.Result().Cookies()[0].Expires.Equal(current.ExpiresAt) {
		t.Fatalf("lost Cookie response not healed: status=%d", w.Code)
	}
	changed := authSessionTestRouter(repo, true, "changed-password")
	if w := authSessionRequest(changed, "GET", "/api/auth/session", "", cookie); w.Code != 401 {
		t.Fatal("password change did not invalidate session")
	}
}

func TestAuthSessionDatabaseFailuresDoNotPretendLogoutOrClearCookie(t *testing.T) {
	repo := &failingAuthSessions{SessionRepository: newAuthSessionTestRepo(t)}
	r := authSessionTestRouter(repo, true, "test-password")
	cookie, _ := authSessionLogin(t, r, nil)
	for _, test := range []struct{ name, method, path, body string }{
		{"create", "POST", "/api/auth/login", `{"username":"admin","password":"test-password"}`},
		{"authenticate", "GET", "/api/auth/session", ""}, {"revoke", "POST", "/api/auth/logout", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo.createErr, repo.authenticateErr, repo.revokeErr = test.name == "create", test.name == "authenticate", test.name == "revoke"
			w := authSessionRequest(r, test.method, test.path, test.body, cookie)
			if w.Code != 503 || len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "private database") || strings.Contains(w.Body.String(), `"ok":true`) {
				t.Fatalf("failure handling: status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	repo.createErr, repo.authenticateErr, repo.revokeErr = false, false, false
	if w := authSessionRequest(r, "GET", "/api/private", "", cookie); w.Code != 200 {
		t.Fatal("failed logout revoked valid session")
	}
}

func TestAuthSessionDisabledAndLoginContentTypeCSRF(t *testing.T) {
	r := authSessionTestRouter(nil, false, "test-password")
	w := authSessionRequest(r, "GET", "/api/auth/session", "", nil)
	var payload map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &payload)
	if w.Code != 200 || payload["authenticated"] != true || payload["auth_enabled"] != false || payload["user"] != "admin" || payload["expires_at"] != nil || len(w.Result().Cookies()) != 0 {
		t.Fatalf("disabled auth fabricated session: %s", w.Body.String())
	}
	r = authSessionTestRouter(newAuthSessionTestRepo(t), true, "test-password")
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=test"} {
		req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
		req.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 415 || len(w.Result().Cookies()) != 0 {
			t.Errorf("accepted form content type %q: %d", contentType, w.Code)
		}
	}
	for _, path := range []string{"/api/auth/login", "/api/auth/logout", "/api/auth/session"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		req.Header.Set("Origin", "https://attacker.example")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 403 || len(w.Result().Cookies()) != 0 {
			t.Errorf("cross-origin %s status=%d", path, w.Code)
		}
	}
}
