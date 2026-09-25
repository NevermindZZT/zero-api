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
