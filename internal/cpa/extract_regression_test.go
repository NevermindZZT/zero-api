package cpa

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractBinaryRejectsTruncatedZip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "truncated.zip")
	dst := filepath.Join(dir, "CLIProxyAPI.exe")
	if err := os.WriteFile(src, []byte{'P', 'K', 0x03, 0x04, 0x00}, 0600); err != nil {
		t.Fatal(err)
	}

	if err := extractBinary(src, dst); err == nil {
		t.Fatal("truncated ZIP must be rejected")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("destination should not be created, stat error = %v", err)
	}
}
