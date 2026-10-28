package diagnostics

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoggerRecreatesExternallyRemovedActiveFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("open-file rename/unlink semantics differ on Windows")
	}
	for _, operation := range []string{"rename", "unlink"} {
		t.Run(operation, func(t *testing.T) {
			opts := logOptions(t)
			l, err := New(opts)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			before := auditEvent()
			before.RequestID = "req_before"
			l.Record(before)
			oldHandle := l.file
			if operation == "rename" {
				err = os.Rename(opts.Path, opts.Path+".external")
			} else {
				err = os.Remove(opts.Path)
			}
			if err != nil {
				t.Fatal(err)
			}
			after := auditEvent()
			after.RequestID = "req_after"
			l.Record(after)
			if l.file == nil || l.file == oldHandle {
				t.Fatal("stale file handle was not replaced")
			}
			if _, err := oldHandle.Stat(); err == nil {
				t.Fatal("stale handle remains open")
			}
			events := readEvents(t, opts.Path)
			if len(events) != 1 || events[0].RequestID != "req_after" {
				t.Fatalf("active log not recreated: %+v", events)
			}
			info, err := os.Stat(opts.Path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatal("recreated file is not private")
			}
			// Subsequent calls continue appending instead of dropping every event.
			after.RequestID = "req_after_again"
			l.Record(after)
			if len(readEvents(t, opts.Path)) != 2 {
				t.Fatal("logging did not recover persistently")
			}
			if operation == "rename" {
				old := readEvents(t, opts.Path+".external")
				if len(old) != 1 || old[0].RequestID != "req_before" {
					t.Fatal("continued writing to externally renamed file")
				}
			}
		})
	}
}
func TestLoggerRuntimeReopenRejectsParentSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require privilege")
	}
	opts := logOptions(t)
	l, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Record(auditEvent())
	dir := filepath.Dir(opts.Path)
	moved := dir + "-external"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	targetFile := filepath.Join(target, filepath.Base(opts.Path))
	if err := os.WriteFile(targetFile, []byte("must stay untouched"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, dir); err != nil {
		t.Fatal(err)
	}
	l.Record(auditEvent())
	l.mu.Lock()
	cleanupErr := l.cleanup(l.oldest)
	l.mu.Unlock()
	if cleanupErr == nil {
		t.Fatal("maintenance accepted unsafe parent directory")
	}
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "must stay untouched" {
		t.Fatal("wrote through parent symlink")
	}
	info, err := os.Stat(targetFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatal("chmod through parent symlink")
	}
	if len(readEvents(t, filepath.Join(moved, filepath.Base(opts.Path)))) != 1 {
		t.Fatal("wrote stale inode through unsafe parent")
	}
	// Restore the original safe directory; diagnostics must recover on next call.
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, dir); err != nil {
		t.Fatal(err)
	}
	l.Record(auditEvent())
	if len(readEvents(t, opts.Path)) != 2 {
		t.Fatal("did not resume after parent restored")
	}
}
func TestLoggerRuntimeReopenRejectsFileSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require privilege")
	}
	opts := logOptions(t)
	l, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Record(auditEvent())
	if err := os.Rename(opts.Path, opts.Path+".external"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "untouched")
	if err := os.WriteFile(target, []byte("must stay untouched"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, opts.Path); err != nil {
		t.Fatal(err)
	}
	l.Record(auditEvent())
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "must stay untouched" {
		t.Fatal("wrote through file symlink")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatal("chmod through file symlink")
	}
}
