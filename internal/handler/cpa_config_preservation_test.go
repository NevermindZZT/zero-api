package handler

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/cpa"
	"github.com/never/zero-api/internal/store"
)

const nativeCPAConfig = `# Settings owned by the native panel
host: "0.0.0.0"
port: 19317
api-keys: [native-one, native-two]
proxy-url: "socks5://127.0.0.1:1080"
request-retry: 7
debug: true
auth-dir: /native/auths
remote-management:
  allow-remote: true
  secret-key: "$2a$10$native-hash-keep-exactly"
  disable-control-panel: false # native preference
routing:
  strategy: fill-first
quota-exceeded:
  switch-project: true
oauth-model-alias:
  codex:
    - name: original-model
      alias: native-model
      fork: true
native-extension:
  nested: [one, two]
`

func newCPAPreservationHandler(t *testing.T, content string) (*CPAHandler, *store.CPAConfigRepo, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := store.NewCPAConfigRepo(db)
	dir := filepath.Join(root, "sidecar")
	if err := repo.Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.EnsureManagementKey(); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return NewCPAHandler(repo, cpa.NewManager(dir, "127.0.0.1", 8317)), repo, filepath.Join(dir, "config.yaml")
}

func cpaConfigRequest(t *testing.T, h *CPAHandler, method, body string) (*httptest.ResponseRecorder, *store.CPAConfig) {
	t.Helper()
	router := gin.New()
	router.GET("/cpa", h.GetConfig)
	router.PUT("/cpa", h.SaveConfig)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, "/cpa", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	var cfg store.CPAConfig
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &cfg); err != nil {
			t.Fatal(err)
		}
	}
	return response, &cfg
}

func readCPATestFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestCPAPrepareAndRestartPreserveNativeSettings(t *testing.T) {
	h, repo, path := newCPAPreservationHandler(t, nativeCPAConfig)
	before, _ := repo.Get()
	for i := 0; i < 2; i++ {
		// Recreate the handler/manager as zero-api does across upgrades/restarts.
		h = NewCPAHandler(repo, cpa.NewManager(filepath.Dir(path), "127.0.0.1", 8317))
		if err := h.PrepareConfig(); err != nil {
			t.Fatal(err)
		}
		if got := string(readCPATestFile(t, path)); got != nativeCPAConfig {
			t.Fatalf("PrepareConfig rewrote native YAML: %s", got)
		}
		cached, err := repo.Get()
		if err != nil {
			t.Fatal(err)
		}
		if cached.Host != "0.0.0.0" || cached.Port != 19317 || cached.RequestRetry != 7 || !cached.Debug || !cached.AllowRemote || cached.ProxyURL != "socks5://127.0.0.1:1080" || !reflect.DeepEqual(cached.APIKeys, []string{"native-one", "native-two"}) {
			t.Fatalf("native shared fields not cached: %+v", cached)
		}
		if cached.ManagementKey != before.ManagementKey || cached.Enabled != before.Enabled || cached.AutoStart != before.AutoStart {
			t.Fatal("native YAML changed DB-only fields")
		}
		status := h.manager.Status()
		if status["host"] != cached.Host || status["port"] != cached.Port {
			t.Fatalf("manager endpoint not refreshed: %+v", status)
		}
	}
}

func TestCPAGetReadsNativeChangesWithoutWriting(t *testing.T) {
	h, repo, path := newCPAPreservationHandler(t, nativeCPAConfig)
	cachedBefore, _ := repo.Get()
	response, cfg := cpaConfigRequest(t, h, http.MethodGet, "")
	if response.Code != http.StatusOK || cfg.Port != 19317 || !cfg.Debug {
		t.Fatalf("GET not using native config: %d %s", response.Code, response.Body.String())
	}
	if string(readCPATestFile(t, path)) != nativeCPAConfig {
		t.Fatal("GET rewrote YAML")
	}
	cachedAfter, _ := repo.Get()
	if !reflect.DeepEqual(cachedBefore, cachedAfter) {
		t.Fatal("GET mutated DB")
	}
	// Native panel edits the same fields after the first GET, without zero-api restart.
	updated := strings.Replace(nativeCPAConfig, "port: 19317", "port: 19318", 1)
	if err := os.WriteFile(path, []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	_, cfg = cpaConfigRequest(t, h, http.MethodGet, "")
	if cfg.Port != 19318 {
		t.Fatal("GET returned stale cached port")
	}
}

func TestCPAPartialSavePreservesNativeSettingsAndReadOnlyFields(t *testing.T) {
	h, repo, path := newCPAPreservationHandler(t, nativeCPAConfig)
	before, _ := repo.Get()
	response, cfg := cpaConfigRequest(t, h, http.MethodPut, `{"request_retry":0,"id":99,"data_dir":"/forbidden","management_key":"replace","created_at":"invalid","updated_at":"invalid"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("PUT failed: %d %s", response.Code, response.Body.String())
	}
	if cfg.RequestRetry != 0 || cfg.Port != 19317 || !cfg.Debug || cfg.ID != before.ID || cfg.DataDir != before.DataDir || !cfg.Enabled || !cfg.AutoStart {
		t.Fatalf("partial PUT lost unrequested values: %+v", cfg)
	}
	file := string(readCPATestFile(t, path))
	for _, native := range []string{"# Settings owned by the native panel", "secret-key: \"$2a$10$native-hash-keep-exactly\"", "auth-dir: /native/auths", "disable-control-panel: false # native preference", "strategy: fill-first", "switch-project: true", "alias: native-model", "fork: true", "nested: [one, two]"} {
		if !strings.Contains(file, native) {
			t.Errorf("native setting/comment missing after partial PUT: %s", native)
		}
	}
	after, _ := repo.Get()
	if after.ManagementKey != before.ManagementKey || after.DataDir != before.DataDir {
		t.Fatal("readonly data or management key changed")
	}
	if strings.Contains(response.Body.String(), before.ManagementKey) {
		t.Fatal("raw management key leaked in config response")
	}
}

func TestCPAPartialSaveDoesNotOverwriteNativeChangesAfterLoad(t *testing.T) {
	h, _, path := newCPAPreservationHandler(t, nativeCPAConfig)
	response, _ := cpaConfigRequest(t, h, http.MethodGet, "")
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	nativeUpdated := strings.Replace(nativeCPAConfig, "port: 19317", "port: 19318", 1)
	nativeUpdated = strings.Replace(nativeUpdated, "debug: true", "debug: false", 1)
	if err := os.WriteFile(path, []byte(nativeUpdated), 0600); err != nil {
		t.Fatal(err)
	}
	response, cfg := cpaConfigRequest(t, h, http.MethodPut, `{"request_retry":0}`)
	if response.Code != http.StatusOK || cfg.Port != 19318 || cfg.Debug || cfg.RequestRetry != 0 {
		t.Fatalf("stale form patch overwrote native changes: %d %s", response.Code, response.Body.String())
	}
}

func TestCPAPartialSaveSupportsExplicitZeroValues(t *testing.T) {
	h, repo, path := newCPAPreservationHandler(t, nativeCPAConfig)
	for _, body := range []string{`{"host":""}`, `{"api_keys":[]}`, `{"proxy_url":""}`, `{"request_retry":0}`, `{"debug":false}`, `{"allow_remote":false}`} {
		response, _ := cpaConfigRequest(t, h, http.MethodPut, body)
		if response.Code != http.StatusOK {
			t.Fatalf("PUT %s failed: %s", body, response.Body.String())
		}
	}
	cfg, _ := repo.Get()
	if cfg.Host != "" || cfg.Port != 19317 || len(cfg.APIKeys) != 0 || cfg.ProxyURL != "" || cfg.RequestRetry != 0 || cfg.Debug || cfg.AllowRemote {
		t.Fatalf("explicit zero values lost: %+v", cfg)
	}
	if !strings.Contains(string(readCPATestFile(t, path)), "native-extension:") {
		t.Fatal("native extension lost")
	}
}

func TestCPALifecycleAndEmptySaveNeverWriteFile(t *testing.T) {
	for _, content := range []string{nativeCPAConfig, ""} {
		h, repo, path := newCPAPreservationHandler(t, content)
		for _, body := range []string{`{"enabled":false,"auto_start":false}`, `{}`, `{"id":99,"data_dir":"/forbidden"}`} {
			response, cfg := cpaConfigRequest(t, h, http.MethodPut, body)
			if response.Code != http.StatusOK || cfg.Enabled || cfg.AutoStart {
				t.Fatalf("lifecycle/empty PUT failed: %d %s", response.Code, response.Body.String())
			}
			if content == "" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("lifecycle save created config: %v", err)
				}
			} else if got := string(readCPATestFile(t, path)); got != content {
				t.Fatal("lifecycle save rewrote config")
			}
		}
		cfg, _ := repo.Get()
		if cfg.Enabled || cfg.AutoStart {
			t.Fatal("lifecycle fields were not persisted")
		}
	}
}

func TestCPABadYAMLNeverChangesFileOrDatabase(t *testing.T) {
	for _, broken := range []string{"port: [broken\n", "remote-management: [wrong-kind]\n"} {
		h, repo, path := newCPAPreservationHandler(t, broken)
		before, _ := repo.Get()
		for _, body := range []string{`{"debug":false}`, `{"enabled":false}`, `{}`} {
			response, _ := cpaConfigRequest(t, h, http.MethodPut, body)
			if response.Code < 400 {
				t.Fatalf("invalid YAML accepted: %s", response.Body.String())
			}
		}
		if err := h.PrepareConfig(); err == nil {
			t.Fatal("PrepareConfig accepted invalid YAML")
		}
		if got := string(readCPATestFile(t, path)); got != broken {
			t.Fatal("invalid YAML was overwritten")
		}
		after, _ := repo.Get()
		if !reflect.DeepEqual(before, after) {
			t.Fatal("bad YAML changed DB cache")
		}
	}
}

func TestCPAInvalidJSONNeverChangesConfig(t *testing.T) {
	h, repo, path := newCPAPreservationHandler(t, nativeCPAConfig)
	before, _ := repo.Get()
	for _, body := range []string{`null`, `[]`, `{"debug":null}`, `{"port":"8317"}`, `{"api_keys":null}`, `{"port":0}`, `{"port":65536}`, `{"request_retry":-1}`} {
		response, _ := cpaConfigRequest(t, h, http.MethodPut, body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid JSON %s accepted: %d", body, response.Code)
		}
	}
	after, _ := repo.Get()
	if !reflect.DeepEqual(before, after) || string(readCPATestFile(t, path)) != nativeCPAConfig {
		t.Fatal("invalid JSON changed configuration")
	}
}

func TestCPANativeProxiesUseFilePortAndRejectBadYAML(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Path))
	}))
	defer upstream.Close()
	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	content := strings.Replace(nativeCPAConfig, "port: 19317", "port: "+strconv.Itoa(port), 1)
	h, repo, path := newCPAPreservationHandler(t, content)
	before, _ := repo.Get()
	if before.Port == port {
		t.Fatal("test requires a stale DB port")
	}
	router := gin.New()
	router.GET("/cpa-native/management.html", h.NativeManagementPanel)
	router.Any("/cpa-native/v0/management/*path", h.NativeManagementResource)
	for requestPath, expected := range map[string]string{
		"/cpa-native/management.html":           "/management.html",
		"/cpa-native/v0/management/config.yaml": "/v0/management/config.yaml",
	} {
		response := &closeNotifyingRecorder{ResponseRecorder: httptest.NewRecorder(), closeNotify: make(chan bool)}
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if response.Code != http.StatusOK || response.Body.String() != expected {
			t.Fatalf("proxy used stale DB endpoint: %d %s", response.Code, response.Body.String())
		}
	}
	after, _ := repo.Get()
	if !reflect.DeepEqual(before, after) || string(readCPATestFile(t, path)) != content {
		t.Fatal("proxy read mutated file or DB")
	}
	if err := os.WriteFile(path, []byte("port: [broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, requestPath := range []string{"/cpa-native/management.html", "/cpa-native/v0/management/config.yaml"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "CPA 配置不可用") {
			t.Fatalf("proxy did not reject damaged YAML: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestCPAStartupPreparesBeforeClientsAndAutostart(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "cmd", "server", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	prepare := bytes.Index(source, []byte("cpaH.PrepareConfig()"))
	if prepare < 0 {
		t.Fatal("startup must prepare config before clients and autostart")
	}
	reread := bytes.Index(source[prepare:], []byte("svc.CPAConfig.Get()"))
	client := bytes.Index(source, []byte("cpa.NewManagementClient("))
	start := bytes.Index(source, []byte("cpaManager.Start()"))
	if prepare < 0 || reread < 0 || client < prepare+reread || start < client {
		t.Fatal("startup must prepare, reread effective DB, construct clients, then autostart")
	}
}
