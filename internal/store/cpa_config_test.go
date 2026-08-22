package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCPAConfigRepoEnsureManagementKeyStable(t *testing.T) {
	db := openTestDB(t)
	repo := NewCPAConfigRepo(db)
	if err := repo.Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	first, err := repo.EnsureManagementKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 40 || strings.TrimSpace(first) != first {
		t.Fatalf("management key = %q, want high-entropy non-empty key", first)
	}

	second, err := repo.EnsureManagementKey()
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("management key changed: first=%q second=%q", first, second)
	}
}
