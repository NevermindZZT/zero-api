package cpa

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyTempFile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "CLIProxyAPI.tmp")
	current := filepath.Join(dir, "CLIProxyAPI.exe.tmp")
	if err := os.WriteFile(legacy, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyTempFile(current); err != nil {
		t.Fatalf("migrateLegacyTempFile failed: %v", err)
	}
	content, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "partial" {
		t.Fatalf("migrated content = %q, want partial", content)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy temp file should be moved, stat error = %v", err)
	}
}
