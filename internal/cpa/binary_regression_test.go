package cpa

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractBinaryFromWindowsZipWithoutUnixMode(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	entry, err := zw.Create("cli-proxy-api.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("windows-binary")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "release.zip")
	dst := filepath.Join(dir, "CLIProxyAPI.exe")
	if err := os.WriteFile(src, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}

	if err := extractBinary(src, dst); err != nil {
		t.Fatalf("Windows ZIP should be extractable: %v", err)
	}
	content, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "windows-binary" {
		t.Fatalf("extracted content = %q, want %q", content, "windows-binary")
	}
}

func TestDownloadFileRemovesPartialFileAfterRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("partial-content"))
	}))
	defer server.Close()

	dst := filepath.Join(t.TempDir(), "download.tmp")
	err := downloadFile(server.URL, dst, int64(len("partial-content")+1), "direct")
	if err == nil {
		t.Fatal("expected size mismatch error")
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("final partial file should be removed, stat error = %v", statErr)
	}
	if _, statErr := os.Stat(dst + ".part"); !os.IsNotExist(statErr) {
		t.Fatalf("partial download file should be removed, stat error = %v", statErr)
	}
}

func TestDownloadOnceResumesExistingPartialFile(t *testing.T) {
	const payload = "complete-content"
	const partial = "complete-"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=9-" {
			w.Header().Set("Content-Length", "16")
			_, _ = w.Write([]byte(partial))
			return
		}
		w.WriteHeader(http.StatusPartialContent)
		w.Header().Set("Content-Range", "bytes 9-15/16")
		_, _ = w.Write([]byte("content"))
	}))
	defer server.Close()

	dst := filepath.Join(t.TempDir(), "download.part")
	if err := os.WriteFile(dst, []byte(partial), 0600); err != nil {
		t.Fatal(err)
	}
	if err := downloadOnce(server.URL, dst, int64(len(payload)), "direct"); err != nil {
		t.Fatalf("expected resume to complete: %v", err)
	}
	content, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != payload {
		t.Fatalf("downloaded content = %q, want %q", content, payload)
	}
}
