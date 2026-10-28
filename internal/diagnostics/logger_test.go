package diagnostics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func logOptions(t *testing.T) Options {
	t.Helper()
	return Options{Path: filepath.Join(t.TempDir(), "logs", "request-errors.jsonl"), MaxSizeBytes: 2048, MaxBackups: 2, MaxAgeDays: 14}
}
func auditEvent() Event {
	return Event{RequestID: "req_test", UpstreamRequestID: "req_upstream", Method: "POST", Path: "/v1/responses", Protocol: "responses", Model: "gpt-5.4", Stage: "attempt", Category: "stream_truncated", Status: 200, UpstreamStatus: 200}
}
func readEvents(t *testing.T, path string) []Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result []Event
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		if len(scanner.Bytes())+1 > MaxEventBytes {
			t.Fatal("unbounded event")
		}
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("invalid JSONL: %v", err)
		}
		result = append(result, e)
	}
	if scanner.Err() != nil {
		t.Fatal(scanner.Err())
	}
	return result
}
func TestLoggerSanitizesAllUntrustedStrings(t *testing.T) {
	opts := logOptions(t)
	l, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	e := auditEvent()
	e.RequestID = "Bearer SECRET_AUTH"
	e.UpstreamRequestID = "sk-SECRET_KEY"
	e.Path = "/v1/responses?token=SECRET_QUERY"
	e.Model = "https://private/SECRET_BODY"
	e.Category = "upstream says SECRET_MESSAGE"
	e.ChannelType = "SECRET_CHANNEL"
	e.Protocol = "SECRET_PROTO"
	e.Method = "SECRET_METHOD"
	e.Stage = "SECRET_STAGE"
	l.Record(e)
	b, err := os.ReadFile(opts.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SECRET") {
		t.Fatalf("sensitive value escaped whitelist: %s", b)
	}
	events := readEvents(t, opts.Path)
	if len(events) != 1 || events[0].Path != "/v1/unknown" || events[0].Category != "unknown" {
		t.Fatalf("unexpected sanitized event: %+v", events)
	}
	e = auditEvent()
	e.Model = strings.Repeat("a", 81)
	e.RequestID = strings.Repeat("b", 97)
	e.UpstreamRequestID = strings.Repeat("c", 100000)
	l.Record(e)
	events = readEvents(t, opts.Path)
	if events[1].Model != "" || events[1].RequestID != "" || events[1].UpstreamRequestID != "" {
		t.Fatal("oversized identifier retained")
	}
}
func TestLoggerPrivatePermissionsAndRestartAppend(t *testing.T) {
	opts := logOptions(t)
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte("existing-private-line\n")
	if err := os.WriteFile(opts.Path, original, 0644); err != nil {
		t.Fatal(err)
	}
	l, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(auditEvent())
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(opts.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, original) {
		t.Fatal("existing log was truncated")
	}
	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{opts.Path: 0600, filepath.Dir(opts.Path): 0700} {
			info, _ := os.Stat(path)
			if info.Mode().Perm() != want {
				t.Errorf("%s permissions %o want %o", path, info.Mode().Perm(), want)
			}
		}
	}
	l, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(auditEvent())
	l.Close()
	after, _ := os.ReadFile(opts.Path)
	if len(after) <= len(data) || !bytes.HasPrefix(after, data) {
		t.Fatal("restart did not append")
	}
	l.Record(auditEvent()) // closed and nil receivers must be harmless
	var disabled *Logger
	disabled.Record(auditEvent())
	if disabled.Close() != nil {
		t.Fatal("nil Close")
	}
}
func TestLoggerRotatesDuringConcurrentWritesAndBoundsDisk(t *testing.T) {
	opts := logOptions(t)
	l, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				l.Record(auditEvent())
			}
		}()
	}
	wg.Wait()
	l.Close()
	entries, err := os.ReadDir(filepath.Dir(opts.Path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != opts.MaxBackups+1 {
		t.Fatalf("expected bounded active plus backups: %d", len(entries))
	}
	var total int64
	for _, entry := range entries {
		path := filepath.Join(filepath.Dir(opts.Path), entry.Name())
		info, _ := entry.Info()
		total += info.Size()
		if info.Size() > opts.MaxSizeBytes {
			t.Fatal("file exceeded rotation bound")
		}
		if len(readEvents(t, path)) == 0 {
			t.Fatal("empty rotated file")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatal("insecure backup")
		}
	}
	if total > int64(opts.MaxBackups+1)*opts.MaxSizeBytes {
		t.Fatal("disk bound exceeded")
	}
}
func TestLoggerRemovesExpiredAndExcessBackups(t *testing.T) {
	opts := logOptions(t)
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".1", ".9"} {
		if err := os.WriteFile(opts.Path+suffix, []byte("old\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-15 * 24 * time.Hour)
	if err := os.Chtimes(opts.Path+".1", old, old); err != nil {
		t.Fatal(err)
	}
	l, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for _, suffix := range []string{".1", ".9"} {
		if _, err := os.Stat(opts.Path + suffix); !os.IsNotExist(err) {
			t.Fatal("backup retained outside policy")
		}
	}
	// Active files also expire even when they have not reached the size limit.
	l.oldest = old
	l.Record(auditEvent())
	if len(readEvents(t, opts.Path)) != 1 {
		t.Fatal("active expiry failed")
	}
}
func TestLoggerRejectsSymlinksAndUnsafeOptions(t *testing.T) {
	for _, opts := range []Options{{}, {Path: "x", MaxSizeBytes: -1}, {Path: "x", MaxBackups: -1}, {Path: "x", MaxAgeDays: -1}, {Path: "x", MaxSizeBytes: 16}, {Path: "x", MaxBackups: 101}} {
		if l, err := New(opts); err == nil {
			l.Close()
			t.Fatalf("unsafe options accepted: %+v", opts)
		}
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require privilege")
	}
	for _, kind := range []string{"file", "directory", "backup"} {
		t.Run(kind, func(t *testing.T) {
			opts := logOptions(t)
			target := filepath.Join(t.TempDir(), "target")
			if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(opts.Path), 0700); err != nil {
				t.Fatal(err)
			}
			var link string
			switch kind {
			case "file":
				link = opts.Path
			case "backup":
				link = opts.Path + ".1"
			case "directory":
				if err := os.Remove(filepath.Dir(opts.Path)); err != nil {
					t.Fatal(err)
				}
				link = filepath.Dir(opts.Path)
				target = t.TempDir()
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if l, err := New(opts); err == nil {
				l.Close()
				t.Fatal("unsafe symlink accepted")
			}
		})
	}
}
func TestLoggerWriteFailureWarnsWithoutSensitiveError(t *testing.T) {
	opts := logOptions(t)
	l, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := os.Rename(opts.Path, opts.Path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(opts.Path, 0700); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&stderr)
	defer log.SetOutput(previous)
	l.Record(Event{Model: "SECRET_BODY", Category: "SECRET_ERROR"})
	if !strings.Contains(stderr.String(), "WARNING") || strings.Contains(stderr.String(), "SECRET") {
		t.Fatalf("unsafe/missing warning: %s", stderr.String())
	}
}

type capturedRecorder struct{ events []Event }

func (r *capturedRecorder) Record(e Event) { r.events = append(r.events, e) }

type untouchedBody struct{ reads int }

func (b *untouchedBody) Read(p []byte) (int, error) { b.reads++; return 0, io.EOF }
func (*untouchedBody) Close() error                 { return nil }
func TestRequestMiddlewareCorrelatesOnlyFailuresWithoutReadingBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := &capturedRecorder{}
	r := gin.New()
	r.Use(RequestMiddleware(rec))
	r.POST("/v1/responses", func(c *gin.Context) {
		if RequestID(c.Request.Context()) == "" {
			t.Error("no request ID")
		}
		c.Status(400)
	})
	body := &untouchedBody{}
	req := httptest.NewRequest("POST", "/v1/responses?token=SECRET_QUERY", nil)
	req.Body = body
	req.Header.Set("Authorization", "Bearer SECRET_TOKEN")
	req.Header.Set("X-Request-Id", "client-SECRET_ID")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if body.reads != 0 || len(rec.events) != 1 {
		t.Fatal("middleware read body or missed failure")
	}
	e := rec.events[0]
	if e.RequestID != w.Header().Get("X-Request-Id") || e.RequestID == "client-SECRET_ID" || e.Stage != "request" || e.Status != 400 || e.Path != "/v1/responses" || e.Category != "invalid_request" {
		t.Fatalf("bad correlation: %+v", e)
	}
	if e.Model != "" {
		t.Fatal("middleware must not read model from body")
	}
	r.GET("/v1/models", func(c *gin.Context) { c.Status(200) })
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/models", nil))
	if len(rec.events) != 1 {
		t.Fatal("success logged as failure")
	}
	if w.Header().Get("X-Request-Id") == e.RequestID {
		t.Fatal("request ID reused")
	}
	r.GET("/api/status", func(c *gin.Context) { c.Status(500) })
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/status", nil))
	if len(rec.events) != 1 || w.Header().Get("X-Request-Id") != "" {
		t.Fatal("non-v1 intercepted")
	}
}
func TestRequestMiddlewareRecordsCancellationAndHidesDynamicPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := &capturedRecorder{}
	r := gin.New()
	r.Use(RequestMiddleware(rec))
	r.GET("/v1/batches/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/batches/SECRET_ID", nil).WithContext(ctx))
	if len(rec.events) != 1 || rec.events[0].Category != "client_cancelled" || rec.events[0].Path != "/v1/unknown" {
		t.Fatalf("unsafe cancellation metadata: %+v", rec.events)
	}
}
func TestRequestMiddlewareNilRecorderStillAddsSafeID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestMiddleware(nil))
	r.GET("/v1/models", func(c *gin.Context) { c.Status(500) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/models", nil))
	if !strings.HasPrefix(w.Header().Get("X-Request-Id"), "req_") {
		t.Fatal("missing safe request ID")
	}
}
