package handler

import (
	"testing"
	"time"

	"github.com/never/zero-api/internal/metrics"
	"github.com/never/zero-api/internal/store"
)

func TestRecordStreamTPSUsesForwardedModel(t *testing.T) {
	monitor := metrics.NewTPSMonitor()
	h := &ProxyHandler{tpsMonitor: monitor}
	completedAt := time.Now()
	observer := metrics.TPSSampleObserver{
		FirstOutputAt: completedAt.Add(-time.Second),
		OutputTokens:  50,
	}

	// 请求可以使用虚拟别名，但路由选中的真实模型应成为 TPS 聚合键。
	h.recordStreamTPS(&store.Model{ModelID: "actual-model"}, observer, completedAt)
	items := monitor.SnapshotAt(5*time.Minute, 10, completedAt)
	if len(items) != 1 || items[0].Model != "actual-model" || items[0].OutputTokens != 50 {
		t.Fatalf("TPS 未按实际模型统计: %+v", items)
	}
}
