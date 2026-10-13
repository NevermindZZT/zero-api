package handler

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
)

type closeNotifyingRecorder struct {
	*httptest.ResponseRecorder
	closeNotify chan bool
}

func (w *closeNotifyingRecorder) CloseNotify() <-chan bool { return w.closeNotify }

func TestNativeManagementResourceProxiesLegacyAndPanelRelativePaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamPaths := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPaths <- r.URL.Path
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = io.WriteString(w, "port: 8317\n")
	}))
	defer upstream.Close()

	_, portString, err := net.SplitHostPort(upstream.Listener.Addr().String())
	if err != nil {
		t.Fatalf("parse test server address: %v", err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "cpa-management.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	configRepo := store.NewCPAConfigRepo(db)
	if err := configRepo.Init(t.TempDir()); err != nil {
		t.Fatalf("init CPA config: %v", err)
	}
	config, err := configRepo.Get()
	if err != nil {
		t.Fatalf("read CPA config: %v", err)
	}
	config.Port = port
	if err := configRepo.Save(config); err != nil {
		t.Fatalf("save CPA config: %v", err)
	}

	router := gin.New()
	h := NewCPAHandler(configRepo, nil)
	router.GET("/cpa-native/management.html", func(c *gin.Context) { c.String(http.StatusOK, "panel") })
	router.Any("/cpa-native/v0/management/*path", h.NativeManagementResource)
	router.Any("/cpa-native/v8/management/*path", h.NativeManagementResource)
	router.Any("/v0/management/*path", h.NativeManagementResource)
	router.Any("/v8/management/*path", h.NativeManagementResource)

	for _, test := range []struct {
		requestPath  string
		upstreamPath string
	}{
		{"/v0/management/config.yaml", "/v0/management/config.yaml"},
		{"/cpa-native/v0/management/config.yaml", "/v0/management/config.yaml"},
		{"/v8/management/config.yaml", "/v8/management/config.yaml"},
		{"/cpa-native/v8/management/config.yaml", "/v8/management/config.yaml"},
	} {
		recorder := &closeNotifyingRecorder{ResponseRecorder: httptest.NewRecorder(), closeNotify: make(chan bool)}
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.requestPath, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s returned status %d: %s", test.requestPath, recorder.Code, recorder.Body.String())
		}
		if recorder.Body.String() != "port: 8317\n" {
			t.Fatalf("GET %s returned unexpected body %q", test.requestPath, recorder.Body.String())
		}
		if got := <-upstreamPaths; got != test.upstreamPath {
			t.Fatalf("GET %s forwarded to %q, want %q", test.requestPath, got, test.upstreamPath)
		}
	}

	panel := httptest.NewRecorder()
	router.ServeHTTP(panel, httptest.NewRequest(http.MethodGet, "/cpa-native/management.html", nil))
	if panel.Code != http.StatusOK || panel.Body.String() != "panel" {
		t.Fatalf("management panel route was shadowed: status=%d body=%q", panel.Code, panel.Body.String())
	}
}
