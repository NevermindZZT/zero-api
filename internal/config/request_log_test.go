package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadRequestLogConfig(t *testing.T, content string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}
func TestRequestLogDefaultsFollowDatabaseDirectory(t *testing.T) {
	for _, tt := range []struct{ yaml, want string }{{"", filepath.Join("data", "logs", "request-errors.jsonl")}, {"database:\n  path: /srv/private/api.db\n", filepath.Join("/srv/private", "logs", "request-errors.jsonl")}} {
		cfg, err := loadRequestLogConfig(t, tt.yaml)
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.RequestLog.IsEnabled() || cfg.RequestLog.Enabled == nil || cfg.RequestLog.Path != tt.want || cfg.RequestLog.MaxSizeMB != 10 || cfg.RequestLog.MaxBackups != 5 || cfg.RequestLog.MaxAgeDays != 14 {
			t.Fatalf("unexpected defaults: %+v", cfg.RequestLog)
		}
	}
}
func TestRequestLogExplicitDisableAndOverrides(t *testing.T) {
	cfg, err := loadRequestLogConfig(t, "request_log:\n  enabled: false\n  path: local/errors.jsonl\n  max_size_mb: 2\n  max_backups: 3\n  max_age_days: 7\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RequestLog.IsEnabled() || cfg.RequestLog.Path != "local/errors.jsonl" || cfg.RequestLog.MaxSizeMB != 2 || cfg.RequestLog.MaxBackups != 3 || cfg.RequestLog.MaxAgeDays != 7 {
		t.Fatalf("explicit config overwritten: %+v", cfg.RequestLog)
	}
}
func TestRequestLogRejectsUnsafeBounds(t *testing.T) {
	for _, field := range []string{"max_size_mb: -1", "max_backups: -1", "max_age_days: -1", "max_size_mb: 1025", "max_backups: 101", "max_age_days: 3651"} {
		if _, err := loadRequestLogConfig(t, "request_log:\n  "+field+"\n"); err == nil || !strings.Contains(err.Error(), "request_log") {
			t.Fatalf("unsafe config accepted %s: %v", field, err)
		}
	}
}
