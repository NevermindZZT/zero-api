package cpa

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractBinaryFromTarUsesOfficialName(t *testing.T) {
	var archive bytes.Buffer
	gw := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{
		Name: "cli-proxy-api",
		Mode: 0755,
		Size: int64(len("binary")),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("binary")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(t.TempDir(), "release.tar.gz")
	dst := filepath.Join(t.TempDir(), "CLIProxyAPI")
	if err := os.WriteFile(src, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}

	if err := extractBinary(src, dst); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "binary" {
		t.Fatalf("extracted content = %q, want %q", content, "binary")
	}
}

func TestIsCPAExecutableRequiresExecutableOfficialName(t *testing.T) {
	for _, test := range []struct {
		name string
		mode int64
		want bool
	}{
		{name: "cli-proxy-api", mode: 0755, want: true},
		{name: "CLIProxyAPI", mode: 0755, want: true},
		{name: "README.md", mode: 0644, want: false},
		{name: "cli-proxy-api", mode: 0644, want: false},
	} {
		if got := isCPAExecutable(test.name, test.mode, true); got != test.want {
			t.Errorf("isCPAExecutable(%q, %#o) = %v, want %v", test.name, test.mode, got, test.want)
		}
	}
}
