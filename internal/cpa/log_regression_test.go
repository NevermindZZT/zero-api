package cpa

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrepareLogFilePreservesPreviousRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpa.log")
	if err := os.WriteFile(path, []byte("old-run\n"), 0644); err != nil {
		t.Fatal(err)
	}
	file, err := prepareLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("new-run\n")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old-run\nnew-run\n" {
		t.Fatalf("previous log truncated: %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe mode %o", info.Mode().Perm())
	}
}

func TestPrepareLogFileRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	path := filepath.Join(dir, "cpa.log")
	if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if file, err := prepareLogFile(path); err == nil {
		file.Close()
		t.Fatal("accepted symlink log")
	}
}
