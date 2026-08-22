package cpa

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestBinaryFileNameMatchesPlatform(t *testing.T) {
	name := binaryFileName()
	if runtime.GOOS == "windows" {
		if name != "CLIProxyAPI.exe" {
			t.Fatalf("Windows binary name = %q, want CLIProxyAPI.exe", name)
		}
		return
	}
	if name != "CLIProxyAPI" {
		t.Fatalf("non-Windows binary name = %q, want CLIProxyAPI", name)
	}
}

func TestNewManagerUsesPlatformBinaryName(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "cliproxyapi"), "127.0.0.1", 8317)
	want := filepath.Join(m.dataDir, binaryFileName())
	if m.BinPath() != want {
		t.Fatalf("manager binary path = %q, want %q", m.BinPath(), want)
	}
}
