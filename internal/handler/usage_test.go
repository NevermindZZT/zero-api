package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/metrics"
)

func TestGetTPSReturnsProcessLocalSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	monitor := metrics.NewTPSMonitor()
	now := time.Now()
	if !monitor.Record("test-model", 30, now.Add(-2*time.Second), now.Add(-time.Second)) {
		t.Fatal("expected TPS sample to be accepted")
	}

	router := gin.New()
	router.GET("/stats/tps", NewUsageHandler(nil, monitor).GetTPS)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/stats/tps?window=1m", nil))
	if recorder.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		WindowSeconds int                     `json:"window_seconds"`
		Items         []metrics.TPSModelStats `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.WindowSeconds != 60 || len(response.Items) != 1 || response.Items[0].Model != "test-model" {
		t.Fatalf("unexpected TPS API response: %+v", response)
	}
}

func TestGetTPSWindowOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	monitor := metrics.NewTPSMonitor()
	now := time.Now()
	for _, age := range []time.Duration{7 * time.Minute, 12 * time.Minute} {
		completedAt := now.Add(-age)
		monitor.Record("actual-model", 40, completedAt.Add(-time.Second), completedAt)
	}
	router := gin.New()
	router.GET("/stats/tps", NewUsageHandler(nil, monitor).GetTPS)

	for _, test := range []struct {
		window  string
		seconds int
		samples int
	}{
		{"5m", 300, 0},
		{"10m", 600, 1},
		{"15m", 900, 2},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest("GET", "/stats/tps?window="+test.window, nil))
		if recorder.Code != 200 {
			t.Fatalf("window=%s status=%d body=%s", test.window, recorder.Code, recorder.Body.String())
		}
		var response struct {
			WindowSeconds int                     `json:"window_seconds"`
			Items         []metrics.TPSModelStats `json:"items"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		count := 0
		if len(response.Items) > 0 {
			count = response.Items[0].SampleCount
			if response.Items[0].Model != "actual-model" {
				t.Fatalf("window=%s model=%q", test.window, response.Items[0].Model)
			}
		}
		if response.WindowSeconds != test.seconds || count != test.samples {
			t.Errorf("window=%s seconds=%d samples=%d, want seconds=%d samples=%d", test.window, response.WindowSeconds, count, test.seconds, test.samples)
		}
	}
}

func TestGetTPSRejectsUnsupportedParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/stats/tps", NewUsageHandler(nil, metrics.NewTPSMonitor()).GetTPS)

	for _, target := range []string{
		"/stats/tps?window=2m",
		"/stats/tps?limit=0",
		"/stats/tps?limit=51",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest("GET", target, nil))
		if recorder.Code != 400 {
			t.Errorf("GET %s status = %d, want 400", target, recorder.Code)
		}
	}
}

func TestGetLatencyStatsRejectsInvalidParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/stats/latency", NewUsageHandler(nil, nil).GetLatencyStats)

	for _, target := range []string{
		"/stats/latency?start=not-a-date",
		"/stats/latency?start=2026-08-04&end=2026-08-03",
		"/stats/latency?granularity=year",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest("GET", target, nil))
		if recorder.Code != 400 {
			t.Errorf("GET %s status = %d, want 400", target, recorder.Code)
		}
	}
}
