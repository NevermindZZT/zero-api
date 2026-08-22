package cpa

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareLogFileRotatesOversizedLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cpa.log")
	if err := os.WriteFile(path, make([]byte, maxLogFileSize+1), 0644); err != nil {
		t.Fatal(err)
	}
	file, err := prepareLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("log size after rotation = %d, want 0", info.Size())
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated log missing: %v", err)
	}
}
