package diagnostics

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultMaxSizeBytes int64 = 10 * 1024 * 1024
	DefaultMaxBackups         = 5
	DefaultMaxAgeDays         = 14
	MaxEventBytes             = 2048
)

type Options struct {
	Path         string
	MaxSizeBytes int64
	MaxBackups   int
	MaxAgeDays   int
}

// Logger owns one active file and at most MaxBackups numbered backups. Record
// deliberately returns no error: diagnostics must never fail a model request.
// One Logger must own a path (cross-process writers are not supported).
type Logger struct {
	mu          sync.Mutex
	opts        Options
	file        *os.File
	oldest      time.Time
	closed      bool
	lastWarning time.Time
	stop        chan struct{}
	done        chan struct{}
}

func New(opts Options) (*Logger, error) {
	if opts.Path == "" || opts.MaxSizeBytes < 0 || opts.MaxBackups < 0 || opts.MaxAgeDays < 0 {
		return nil, errors.New("request log: invalid options")
	}
	if opts.MaxSizeBytes == 0 {
		opts.MaxSizeBytes = DefaultMaxSizeBytes
	}
	if opts.MaxBackups == 0 {
		opts.MaxBackups = DefaultMaxBackups
	}
	if opts.MaxAgeDays == 0 {
		opts.MaxAgeDays = DefaultMaxAgeDays
	}
	if opts.MaxSizeBytes < 1024 || opts.MaxSizeBytes > 1<<30 || opts.MaxBackups > 100 || opts.MaxAgeDays > 3650 {
		return nil, errors.New("request log: options outside safety bounds")
	}
	abs, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, errors.New("request log: invalid path")
	}
	opts.Path = abs
	if err = secureDirectory(filepath.Dir(abs), filepath.Base(abs)); err != nil {
		return nil, err
	}
	l := &Logger{opts: opts, stop: make(chan struct{}), done: make(chan struct{})}
	if err = l.cleanup(time.Now()); err != nil {
		return nil, err
	}
	if err = l.open(); err != nil {
		return nil, err
	}
	info, err := l.file.Stat()
	if err != nil {
		l.file.Close()
		return nil, errors.New("request log: cannot inspect active file")
	}
	if info.Size() > opts.MaxSizeBytes || l.expired(time.Now()) {
		if err = l.rotate(time.Now()); err != nil {
			if l.file != nil {
				l.file.Close()
			}
			return nil, err
		}
	}
	go l.maintain()
	return l, nil
}

// secureDirectory rejects symlink components and tightens the log directory,
// not its pre-existing parents. Newly created directories are private as well.
func secureDirectory(dir, basename string) error {
	if filepath.Dir(dir) == dir {
		return errors.New("request log: use a dedicated private directory")
	}
	current := dir
	for {
		info, err := os.Lstat(current)
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir()) {
			return errors.New("request log: unsafe directory")
		}
		if err != nil && !os.IsNotExist(err) {
			return errors.New("request log: directory inaccessible")
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	if info, err := os.Lstat(dir); err == nil && info.Mode().Perm() != 0700 {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return errors.New("request log: directory inaccessible")
		}
		for _, entry := range entries {
			if entry.Name() == basename {
				continue
			}
			suffix := strings.TrimPrefix(entry.Name(), basename+".")
			n, parseErr := strconv.Atoi(suffix)
			if !strings.HasPrefix(entry.Name(), basename+".") || parseErr != nil || n < 1 || strconv.Itoa(n) != suffix {
				return errors.New("request log: use a dedicated private directory")
			}
		}
	}
	if os.MkdirAll(dir, 0700) != nil || os.Chmod(dir, 0700) != nil {
		return errors.New("request log: cannot secure directory")
	}
	return nil
}
func secureFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("request log: unsafe or inaccessible file")
	}
	if os.Chmod(path, 0600) != nil {
		return nil, errors.New("request log: cannot secure file")
	}
	return info, nil
}
func (l *Logger) open() error {
	if err := secureDirectory(filepath.Dir(l.opts.Path), filepath.Base(l.opts.Path)); err != nil {
		return err
	}
	before, err := secureFile(l.opts.Path)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(l.opts.Path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return errors.New("request log: cannot open active file")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || (before != nil && !os.SameFile(before, info)) {
		f.Close()
		return errors.New("request log: active file changed")
	}
	after, err := os.Lstat(l.opts.Path)
	if err != nil || !os.SameFile(info, after) || after.Mode()&os.ModeSymlink != 0 || f.Chmod(0600) != nil {
		f.Close()
		return errors.New("request log: cannot secure active file")
	}
	l.file = f
	l.oldest = info.ModTime()
	if info.Size() == 0 {
		l.oldest = time.Now()
		return nil
	}
	// The first event provides a conservative age across restarts; never read
	// an unbounded old line or copy old contents into diagnostics/stderr.
	line, _ := bufio.NewReader(io.NewSectionReader(f, 0, MaxEventBytes)).ReadBytes('\n')
	var first struct {
		Timestamp time.Time `json:"timestamp"`
	}
	if json.Unmarshal(line, &first) == nil && !first.Timestamp.IsZero() && first.Timestamp.Before(time.Now()) {
		l.oldest = first.Timestamp
	}
	return nil
}
func (l *Logger) expired(now time.Time) bool {
	return !l.oldest.IsZero() && now.Sub(l.oldest) >= time.Duration(l.opts.MaxAgeDays)*24*time.Hour
}
func (l *Logger) backup(n int) string { return fmt.Sprintf("%s.%d", l.opts.Path, n) }

func (l *Logger) cleanup(now time.Time) error {
	if err := secureDirectory(filepath.Dir(l.opts.Path), filepath.Base(l.opts.Path)); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Dir(l.opts.Path))
	if err != nil {
		return errors.New("request log: cannot inspect backups")
	}
	prefix := filepath.Base(l.opts.Path) + "."
	cutoff := now.Add(-time.Duration(l.opts.MaxAgeDays) * 24 * time.Hour)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		suffix := strings.TrimPrefix(entry.Name(), prefix)
		n, err := strconv.Atoi(suffix)
		if err != nil || n < 1 || strconv.Itoa(n) != suffix {
			continue
		}
		path := filepath.Join(filepath.Dir(l.opts.Path), entry.Name())
		info, err := secureFile(path)
		if err != nil {
			return err
		}
		if info == nil {
			continue
		}
		// Older configurations may have used larger files or more backups. Keep
		// the current disk bound after a configuration change as well.
		if n > l.opts.MaxBackups || info.ModTime().Before(cutoff) || info.Size() > l.opts.MaxSizeBytes {
			if os.Remove(path) != nil {
				return errors.New("request log: cannot remove expired backup")
			}
		}
	}
	return nil
}
func (l *Logger) rotate(now time.Time) error {
	if err := l.cleanup(now); err != nil {
		return err
	}
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			l.file = nil
			return errors.New("request log: cannot close active file")
		}
		l.file = nil
	}
	for n := l.opts.MaxBackups; n >= 1; n-- {
		path := l.backup(n)
		info, err := secureFile(path)
		if err != nil {
			return err
		}
		if info == nil {
			continue
		}
		if n == l.opts.MaxBackups {
			if os.Remove(path) != nil {
				return errors.New("request log: cannot remove oldest backup")
			}
		} else if os.Rename(path, l.backup(n+1)) != nil {
			return errors.New("request log: cannot rotate backup")
		}
	}
	info, err := secureFile(l.opts.Path)
	if err != nil {
		return err
	}
	if info != nil {
		if os.Rename(l.opts.Path, l.backup(1)) != nil {
			return errors.New("request log: cannot rotate active file")
		}
		if !l.oldest.IsZero() && os.Chtimes(l.backup(1), l.oldest, l.oldest) != nil {
			return errors.New("request log: cannot timestamp backup")
		}
	}
	if err = l.cleanup(now); err != nil {
		return err
	}
	return l.open()
}
func (l *Logger) warn() {
	if time.Since(l.lastWarning) >= time.Minute {
		// No err.Error(), path, request body, or upstream message is interpolated.
		log.Print("[request-log] WARNING: diagnostic log write/rotation failed; request processing continues (further warnings rate-limited)")
		l.lastWarning = time.Now()
	}
}
func (l *Logger) Record(e Event) {
	if l == nil {
		return
	}
	line, err := json.Marshal(normalize(e))
	if err != nil || len(line)+1 > MaxEventBytes {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.warn()
		return
	}
	line = append(line, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	if l.file == nil {
		if err = l.open(); err != nil {
			l.warn()
			return
		}
	}
	// Verify the parent path as well: an external rotation may replace the
	// directory, not just the file. Never chmod/open through a parent symlink.
	if err = secureDirectory(filepath.Dir(l.opts.Path), filepath.Base(l.opts.Path)); err != nil {
		l.warn()
		return
	}
	// Verify the on-disk file still names our regular private file before each
	// append. Missing/replaced files are reopened rather than writing through
	// a stale fd (including an unlinked inode).
	info, err := secureFile(l.opts.Path)
	current, statErr := l.file.Stat()
	if err != nil {
		l.warn()
		return
	}
	if info == nil || statErr != nil || !os.SameFile(info, current) {
		l.file.Close()
		l.file = nil
		if l.open() != nil {
			l.warn()
			return
		}
		current, _ = l.file.Stat()
	}
	if len(line) > int(l.opts.MaxSizeBytes) {
		l.warn()
		return
	}
	now := time.Now()
	if err = l.cleanup(now); err != nil {
		l.warn()
		return
	}
	if current == nil || current.Size()+int64(len(line)) > l.opts.MaxSizeBytes || (current.Size() > 0 && l.expired(now)) {
		if err = l.rotate(now); err != nil {
			l.warn()
			return
		}
	}
	n, err := l.file.Write(line)
	if err != nil || n != len(line) {
		l.warn()
	}
}
func (l *Logger) maintain() {
	defer close(l.done)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case now := <-ticker.C:
			l.mu.Lock()
			if !l.closed {
				if l.expired(now) {
					if err := l.rotate(now); err != nil {
						l.warn()
					}
				} else if err := l.cleanup(now); err != nil {
					l.warn()
				}
			}
			l.mu.Unlock()
		}
	}
}
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	close(l.stop)
	var err error
	if l.file != nil {
		if l.file.Close() != nil {
			err = errors.New("request log: close failed")
		}
		l.file = nil
	}
	l.mu.Unlock()
	<-l.done
	return err
}
