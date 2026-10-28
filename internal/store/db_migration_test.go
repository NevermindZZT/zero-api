package store

import (
	"path/filepath"
	"testing"
)

func TestFreshDatabaseIsFullyUsableOnFirstOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := NewService(db)
	svc.Channel.InvalidateChannelCache()
	svc.Model.InvalidateModelCache()
	defer svc.Channel.InvalidateChannelCache()
	defer svc.Model.InvalidateModelCache()
	id, err := svc.Channel.Create(&Channel{Name: "test", Type: "responses", BaseURL: "http://127.0.0.1:8317", Status: "active", FailoverEnabled: true})
	if err != nil {
		t.Fatalf("first boot channel create: %v", err)
	}
	if _, err = svc.Channel.GetByID(id); err != nil {
		t.Fatalf("first boot channel read: %v", err)
	}
	if _, err = svc.ProxyConfig.Get(); err != nil {
		t.Fatalf("first boot proxy settings: %v", err)
	}
	if _, err = svc.Model.List(0); err != nil {
		t.Fatalf("first boot model list: %v", err)
	}
	for _, query := range []string{"SELECT user_modified, protocol_urls, capabilities FROM models LIMIT 1", "SELECT commit_sha FROM skills LIMIT 1", "SELECT total_duration_ms FROM usage_records LIMIT 1"} {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
	}
}

func TestDatabaseMigrationsAreIdempotentAndRetainData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO channels(name,base_url) VALUES('preserved','http://localhost')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`ALTER TABLE channels DROP COLUMN priority`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		db, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var priority int
		var name string
		if err = db.QueryRow(`SELECT name,priority FROM channels`).Scan(&name, &priority); err != nil {
			t.Fatal(err)
		}
		if name != "preserved" || priority != 99 {
			t.Fatalf("data/column lost: %q %d", name, priority)
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
