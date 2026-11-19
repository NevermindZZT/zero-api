package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Enable foreign keys and a busy timeout on every pooled connection, not only
// the first connection configured by Open.
func setupSyncUsageDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "sync-usage.db"))
	db, err := Open("file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO channels(id,name,type,base_url) VALUES(1,'test','openai','http://example.test')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func insertSyncModel(t *testing.T, db *DB, id int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO models(id,channel_id,model_id) VALUES(?,1,?)`, id, fmt.Sprintf("m-%d", id)); err != nil {
		t.Fatal(err)
	}
}

func syncUsage(model int64, channel int64) *UsageRecord {
	key := int64(10)
	return &UsageRecord{ChannelID: &channel, ModelID: &model, APIKeyID: &key, RequestModel: "requested-model", PromptTokens: 100, CompletionTokens: 25, CacheHitTokens: 50, TotalTokens: 125, Cost: 0.125, LatencyMs: 31, TotalDurationMs: 55}
}

func TestSyncUsageQueuedAndDirectAfterModelDeletion(t *testing.T) {
	db := setupSyncUsageDB(t)
	repo := NewUsageRepo(db)
	insertSyncModel(t, db, 101)
	queued := syncUsage(101, 1)
	if _, err := repo.Insert(queued); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM models WHERE id=101`); err != nil {
		t.Fatal(err)
	}
	select {
	case record := <-usageBuffer:
		if record != queued {
			t.Fatal("unexpected buffered record")
		}
		flushUsageBatch(db, []*UsageRecord{record})
	case <-time.After(time.Second):
		t.Fatal("record was not queued")
	}
	if _, err := repo.insertDirect(syncUsage(101, 1)); err != nil {
		t.Fatalf("direct stale model: %v", err)
	}
	rows, err := db.Query(`SELECT model_id,request_model,prompt_tokens,completion_tokens,cache_hit_tokens,total_tokens,cost FROM usage_records ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var model sql.NullInt64
		var name string
		var input, output, cached, total int
		var cost float64
		if err := rows.Scan(&model, &name, &input, &output, &cached, &total, &cost); err != nil {
			t.Fatal(err)
		}
		if model.Valid || name != "requested-model" || input != 100 || output != 25 || cached != 50 || total != 125 || cost != 0.125 {
			t.Fatalf("incorrect usage: %v %s %d %d %d %d %f", model, name, input, output, cached, total, cost)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("got %d records, want 2", count)
	}
	var requests, total int
	var cost float64
	if err := db.QueryRow(`SELECT SUM(requests),SUM(total_tokens),SUM(cost) FROM usage_daily WHERE api_key_id=10 AND request_model='requested-model'`).Scan(&requests, &total, &cost); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || total != 250 || cost != 0.25 {
		t.Fatalf("aggregate requests=%d total=%d cost=%f", requests, total, cost)
	}
}

func TestSyncUsageFailedDetailDoesNotAggregate(t *testing.T) {
	db := setupSyncUsageDB(t)
	insertSyncModel(t, db, 101)
	good, bad := syncUsage(101, 1), syncUsage(101, 999)
	flushUsageBatch(db, []*UsageRecord{good, bad})
	if _, err := NewUsageRepo(db).insertDirect(bad); err == nil {
		t.Fatal("expected foreign key error")
	}
	var details, requests, tokens int
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_records`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COALESCE(SUM(requests),0),COALESCE(SUM(total_tokens),0) FROM usage_daily`).Scan(&requests, &tokens); err != nil {
		t.Fatal(err)
	}
	if details != 1 || requests != 1 || tokens != 125 {
		t.Fatalf("details=%d aggregate requests=%d tokens=%d", details, requests, tokens)
	}
}

func TestSyncUsageAggregateFailurePreservesDetails(t *testing.T) {
	db := setupSyncUsageDB(t)
	insertSyncModel(t, db, 101)
	if _, err := db.Exec(`CREATE TRIGGER fail_usage_daily BEFORE INSERT ON usage_daily WHEN NEW.request_model='bad-aggregate' BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	good, bad := syncUsage(101, 1), syncUsage(101, 1)
	bad.RequestModel = "bad-aggregate"
	// The first aggregate succeeds, and the second fails; neither may remain.
	flushUsageBatch(db, []*UsageRecord{good, bad})
	id, err := NewUsageRepo(db).insertDirect(bad)
	if err != nil || id == 0 {
		t.Fatalf("direct detail should commit despite aggregate failure: id=%d err=%v", id, err)
	}
	var details, aggregates, tokens int
	var cost float64
	if err := db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(total_tokens),0),COALESCE(SUM(cost),0) FROM usage_records`).Scan(&details, &tokens, &cost); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_daily`).Scan(&aggregates); err != nil {
		t.Fatal(err)
	}
	if details != 3 || tokens != 375 || cost != 0.375 || aggregates != 0 {
		t.Fatalf("details=%d tokens=%d cost=%f aggregates=%d", details, tokens, cost, aggregates)
	}
}

func TestSyncUsageMissingAggregateTablePreservesDetails(t *testing.T) {
	db := setupSyncUsageDB(t)
	insertSyncModel(t, db, 101)
	if _, err := db.Exec(`DROP TABLE usage_daily`); err != nil {
		t.Fatal(err)
	}
	flushUsageBatch(db, []*UsageRecord{syncUsage(101, 1)})
	if _, err := NewUsageRepo(db).insertDirect(syncUsage(101, 1)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_records`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("details=%d; want 2", count)
	}
}

func TestSyncUsageExistingAndNilModel(t *testing.T) {
	db := setupSyncUsageDB(t)
	insertSyncModel(t, db, 101)
	repo := NewUsageRepo(db)
	if _, err := repo.insertDirect(syncUsage(101, 1)); err != nil {
		t.Fatal(err)
	}
	noModel := syncUsage(101, 1)
	noModel.ModelID = nil
	flushUsageBatch(db, []*UsageRecord{noModel})
	var live, missing int
	if err := db.QueryRow(`SELECT SUM(model_id=101),SUM(model_id IS NULL) FROM usage_records`).Scan(&live, &missing); err != nil {
		t.Fatal(err)
	}
	if live != 1 || missing != 1 {
		t.Fatalf("live=%d null=%d", live, missing)
	}
}

func TestSyncUsageIgnoredInsertDoesNotAggregate(t *testing.T) {
	db := setupSyncUsageDB(t)
	insertSyncModel(t, db, 101)
	if _, err := db.Exec(`CREATE TRIGGER ignore_usage BEFORE INSERT ON usage_records BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	record := syncUsage(101, 1)
	flushUsageBatch(db, []*UsageRecord{record})
	if _, err := NewUsageRepo(db).insertDirect(record); err == nil {
		t.Fatal("expected ignored direct insert to fail")
	}
	var details, aggregates int
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_records`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_daily`).Scan(&aggregates); err != nil {
		t.Fatal(err)
	}
	if details != 0 || aggregates != 0 {
		t.Fatalf("details=%d aggregates=%d", details, aggregates)
	}
}

func TestSyncUsageConcurrentDeleteAndWrite(t *testing.T) {
	db := setupSyncUsageDB(t)
	insertSyncModel(t, db, 101)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan error, 3)
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-start
		_, err := NewUsageRepo(db).insertDirect(syncUsage(101, 1))
		errCh <- err
	}()
	go func() { defer wg.Done(); <-start; flushUsageBatch(db, []*UsageRecord{syncUsage(101, 1)}) }()
	go func() { defer wg.Done(); <-start; _, err := db.Exec(`DELETE FROM models WHERE id=101`); errCh <- err }()
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	var details, dangling, requests int
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(model_id IS NOT NULL),0) FROM usage_records`).Scan(&details, &dangling); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COALESCE(SUM(requests),0) FROM usage_daily`).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if details != 2 || dangling != 0 || requests != 2 {
		t.Fatalf("details=%d dangling=%d aggregate=%d", details, dangling, requests)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key check failed")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
