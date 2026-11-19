package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/store"
)

func TestAPIKeyOptionsAndHeatmapFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := store.Open(filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	keys := store.NewAPIKeyRepo(db)
	first, err := keys.Create("First")
	if err != nil {
		t.Fatal(err)
	}
	second, err := keys.Create("Disabled")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := keys.Create("Former")
	if err != nil {
		t.Fatal(err)
	}
	if err := keys.Toggle(second.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	for _, record := range []struct {
		key    int64
		tokens int
	}{{first.ID, 10}, {second.ID, 20}, {deleted.ID, 30}, {deleted.ID, 40}} {
		if _, err := db.Exec(`INSERT INTO usage_records (api_key_id, request_model, total_tokens, created_at) VALUES (?, 'test', ?, ?)`, record.key, record.tokens, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := keys.Delete(deleted.ID); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	api := router.Group("/api")
	keyHandler := NewAPIKeyHandler(keys)
	api.GET("/api-keys", keyHandler.List)
	api.GET("/api-keys/options", keyHandler.ListOptions)
	// Match production routes to ensure the static options endpoint does not conflict.
	api.PUT("/api-keys/:id", keyHandler.UpdateConfig)
	api.POST("/api-keys/:id/toggle", keyHandler.Toggle)
	api.DELETE("/api-keys/:id", keyHandler.Delete)
	api.GET("/stats/year-heatmap", NewUsageHandler(store.NewUsageRepo(db), nil).GetYearHeatmap)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/api-keys/options", nil))
	if rec.Code != 200 {
		t.Fatalf("options status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), first.Key) || strings.Contains(rec.Body.String(), second.Key) || strings.Contains(rec.Body.String(), `"key"`) {
		t.Fatalf("options leaked credentials: %s", rec.Body.String())
	}
	var options []store.APIKeyOption
	if err := json.Unmarshal(rec.Body.Bytes(), &options); err != nil {
		t.Fatal(err)
	}
	if len(options) != 3 || options[0] != (store.APIKeyOption{ID: first.ID, Name: "First", Enabled: true}) || options[1] != (store.APIKeyOption{ID: second.ID, Name: "Disabled"}) || options[2] != (store.APIKeyOption{ID: deleted.ID, Deleted: true}) {
		t.Fatalf("options=%+v", options)
	}

	for _, tc := range []struct {
		id    string
		total int64
	}{{"", 100}, {fmt.Sprint(first.ID), 10}, {fmt.Sprint(second.ID), 20}, {fmt.Sprint(deleted.ID), 70}, {"99999", 0}} {
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stats/year-heatmap?tz_offset=0&api_key_id="+tc.id, nil))
		if rec.Code != 200 {
			t.Fatalf("heatmap id=%s status=%d body=%s", tc.id, rec.Code, rec.Body.String())
		}
		var items []store.YearHeatmapItem
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		var total int64
		for _, item := range items {
			total += item.TotalTokens
		}
		if total != tc.total {
			t.Fatalf("heatmap id=%s total=%d want %d", tc.id, total, tc.total)
		}
	}
}

func TestStatsHourlyDateRangeValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := store.Open(filepath.Join(t.TempDir(), "hour-range.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := NewUsageHandler(store.NewUsageRepo(db), nil)
	router := gin.New()
	router.GET("/stats/daily", h.GetDailyStats)
	router.GET("/stats/latency", h.GetLatencyStats)
	cases := []struct {
		query           string
		status, buckets int
	}{
		{"start=2024-02-01&end=2024-03-02&granularity=hour", 200, 744}, // 31 days including leap day
		{"start=2024-02-01&end=2024-03-03&granularity=hour", 400, 0},   // 32 days
		{"start=2024-02-01&end=2024-02-01&granularity=hour", 200, 24},
		{"start=2024-02-30&end=2024-03-02&granularity=hour", 400, 0},
		{"start=2024-02-01&end=invalid&granularity=hour", 400, 0},
		{"start=2024-03-02&end=2024-02-01&granularity=hour", 400, 0},
		{"start=0001-01-01&end=9999-12-31&granularity=hour", 400, 0},
		{"start=2024-02-01&end=2024-03-03&granularity=day", 200, 32},
	}
	for _, path := range []string{"/stats/daily", "/stats/latency"} {
		for _, tc := range cases {
			t.Run(path+"?"+tc.query, func(t *testing.T) {
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+tc.query, nil))
				if rec.Code != tc.status {
					t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.status, rec.Body.String())
				}
				if tc.status == 200 {
					var buckets []json.RawMessage
					if err := json.Unmarshal(rec.Body.Bytes(), &buckets); err != nil {
						t.Fatal(err)
					}
					if len(buckets) != tc.buckets {
						t.Fatalf("buckets=%d want=%d", len(buckets), tc.buckets)
					}
				}
			})
		}
	}
}

func TestAPIKeyOptionsReturnsEmptyArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := store.Open(filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	router := gin.New()
	router.GET("/api/api-keys/options", NewAPIKeyHandler(store.NewAPIKeyRepo(db)).ListOptions)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/api-keys/options", nil))
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
