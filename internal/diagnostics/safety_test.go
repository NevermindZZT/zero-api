package diagnostics

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoggerDoesNotChmodSharedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission-specific test")
	}
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "database.db"), []byte("private database"), 0600); err != nil {
		t.Fatal(err)
	}
	if l, err := New(Options{Path: filepath.Join(dir, "errors.jsonl")}); err == nil {
		l.Close()
		t.Fatal("shared insecure directory accepted")
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatal("changed shared directory permissions")
	}
}
func TestLoggerShrinkingLimitDoesNotRetainOversizedFiles(t *testing.T) {
	opts := logOptions(t)
	if err := os.MkdirAll(filepath.Dir(opts.Path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.Path, make([]byte, opts.MaxSizeBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	l, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Record(auditEvent())
	entries, err := os.ReadDir(filepath.Dir(opts.Path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		info, _ := entry.Info()
		if info.Size() > opts.MaxSizeBytes {
			t.Fatal("oversized legacy log retained outside disk bound")
		}
	}
}
