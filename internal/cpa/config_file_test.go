package cpa

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func nativeConfigFixture() []byte {
	return []byte(`# native panel settings must survive zero-api restarts
host: 0.0.0.0
port: 9317
api-keys: [native-key]
proxy-url: http://native-proxy.invalid:7890
request-retry: 0
debug: true
remote-management:
  allow-remote: true
  secret-key: "$2a$10$native-test-hash"
  disable-control-panel: false
  panel-github-repository: https://example.invalid/native-panel
routing:
  strategy: fill-first
streaming:
  keepalive-seconds: 23
quota-exceeded:
  switch-project: false
oauth-model-alias:
  codex:
    - name: gpt-test
      alias: native-alias
oauth-excluded-models:
  codex: [excluded-native-model]
custom-native-option: &native-defaults
  enabled: true
custom-native-reference: *native-defaults
auth-dir: /native/auth-location
`)
}
func writeNativeFixture(t *testing.T, dir string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath(dir), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func configMap(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestWriteConfigOnlyInitializesMissingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "native: directory")
	cfg := DefaultConfig()
	cfg.ManagementKey = "test-management"
	if err := cfg.WriteConfig(dir); err != nil {
		t.Fatal(err)
	}
	first := fileBytes(t, configPath(dir))
	root := configMap(t, first)
	want, err := filepath.Abs(filepath.Join(dir, "auths"))
	if err != nil {
		t.Fatal(err)
	}
	if root["auth-dir"] != want {
		t.Fatalf("auth path is not safely encoded: %v", root["auth-dir"])
	}
	cfg.ProxyURL = "http://different.invalid"
	cfg.Port = 1234
	if err := cfg.WriteConfig(dir); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, fileBytes(t, configPath(dir))) {
		t.Fatal("restart regenerated existing configuration")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(configPath(dir))
		if info.Mode().Perm() != 0600 {
			t.Fatal("new config permissions not private")
		}
	}
}
func TestRestartPreservesAllNativeSettingsByteForByte(t *testing.T) {
	dir := t.TempDir()
	original := nativeConfigFixture()
	writeNativeFixture(t, dir, original)
	cfg := DefaultConfig()
	cfg.ManagementKey = "database-management-key"
	for i := 0; i < 3; i++ {
		if err := cfg.WriteConfig(dir); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(original, fileBytes(t, configPath(dir))) {
		t.Fatal("native configuration changed on restart")
	}
	if _, err := os.Stat(filepath.Join(dir, ConfigBackupName)); !os.IsNotExist(err) {
		t.Fatal("read-only restart should not create/replace backup")
	}
	effective, err := ReadConfig(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if effective.Host != "0.0.0.0" || effective.Port != 9317 || effective.RequestRetry != 0 || !effective.Debug || !effective.AllowRemote || effective.APIKeys[0] != "native-key" || effective.ProxyURL != "http://native-proxy.invalid:7890" {
		t.Fatal("native shared values not reflected")
	}
	if effective.ManagementKey != cfg.ManagementKey {
		t.Fatal("raw database management key replaced by native hash")
	}
}
func TestPatchOnlyExplicitFieldsAndKeepPrivateBackup(t *testing.T) {
	dir := t.TempDir()
	original := nativeConfigFixture()
	writeNativeFixture(t, dir, original)
	cfg := DefaultConfig()
	cfg.Debug = false
	cfg.ProxyURL = ""
	cfg.APIKeys = []string{}
	cfg.RequestRetry = 0
	cfg.AllowRemote = false
	if err := cfg.PatchConfig(dir, []string{"debug", "proxy_url", "api_keys", "request_retry", "allow_remote"}); err != nil {
		t.Fatal(err)
	}
	updated := fileBytes(t, configPath(dir))
	root := configMap(t, updated)
	if root["host"] != "0.0.0.0" || root["port"] != 9317 {
		t.Fatal("unspecified endpoint overwritten")
	}
	if root["debug"] != false || root["proxy-url"] != "" || root["request-retry"] != 0 || len(root["api-keys"].([]any)) != 0 {
		t.Fatal("explicit zero/false/empty values were ignored")
	}
	management := root["remote-management"].(map[string]any)
	if management["allow-remote"] != false || management["secret-key"] != "$2a$10$native-test-hash" || management["panel-github-repository"] != "https://example.invalid/native-panel" {
		t.Fatal("nested native settings were overwritten")
	}
	before := configMap(t, original)
	for _, key := range []string{"routing", "streaming", "quota-exceeded", "oauth-model-alias", "oauth-excluded-models", "custom-native-option", "custom-native-reference", "auth-dir"} {
		a, _ := yaml.Marshal(before[key])
		b, _ := yaml.Marshal(root[key])
		if !bytes.Equal(a, b) {
			t.Fatalf("native field changed: %s", key)
		}
	}
	if !strings.Contains(string(updated), "# native panel settings") {
		t.Fatal("native comments discarded")
	}
	backup := filepath.Join(dir, ConfigBackupName)
	if !bytes.Equal(fileBytes(t, backup), original) {
		t.Fatal("backup did not retain original configuration")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(backup)
		if info.Mode().Perm() != 0600 {
			t.Fatal("backup permissions not private")
		}
	}
	// A subsequent explicit save still merges the latest native-only edits.
	updated = bytes.ReplaceAll(updated, []byte("keepalive-seconds: 23"), []byte("keepalive-seconds: 45"))
	writeNativeFixture(t, dir, updated)
	cfg.Debug = true
	if err := cfg.PatchConfig(dir, []string{"debug"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fileBytes(t, configPath(dir))), "keepalive-seconds: 45") {
		t.Fatal("later native edit lost")
	}
}
func TestNoopPatchPreservesPresentationAndDoesNotBackup(t *testing.T) {
	dir := t.TempDir()
	data := nativeConfigFixture()
	writeNativeFixture(t, dir, data)
	cfg := DefaultConfig()
	cfg.RequestRetry = 0
	if err := cfg.PatchConfig(dir, []string{"request_retry"}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, fileBytes(t, configPath(dir))) {
		t.Fatal("no-op patch rewrote file")
	}
	if _, err := os.Stat(filepath.Join(dir, ConfigBackupName)); !os.IsNotExist(err) {
		t.Fatal("no-op patch created backup")
	}
}
func TestMalformedNativeConfigIsNeverReplaced(t *testing.T) {
	for _, input := range []string{"", "# only a comment\n", "routing: [broken\n", "port: 8317\nport: 9999\n", "port: 8317\n---\nport: 9999\n", "- not-a-map\n"} {
		t.Run(input, func(t *testing.T) {
			dir := t.TempDir()
			original := []byte(input)
			writeNativeFixture(t, dir, original)
			cfg := DefaultConfig()
			if err := cfg.WriteConfig(dir); err == nil {
				t.Fatal("accepted invalid config")
			}
			if err := cfg.PatchConfig(dir, []string{"debug"}); err == nil {
				t.Fatal("patched invalid config")
			}
			if !bytes.Equal(original, fileBytes(t, configPath(dir))) {
				t.Fatal("invalid file replaced with defaults")
			}
			if _, err := os.Stat(filepath.Join(dir, ConfigBackupName)); !os.IsNotExist(err) {
				t.Fatal("invalid file replaced valid backup")
			}
		})
	}
}
func TestInvalidPatchDoesNotCreateOrModifyFile(t *testing.T) {
	for _, field := range []string{"management_key", "auth-dir", "routing", "unknown"} {
		dir := t.TempDir()
		cfg := DefaultConfig()
		if !errors.Is(cfg.PatchConfig(dir, []string{field}), ErrInvalidConfigField) {
			t.Fatal("accepted nonshared field")
		}
		if _, err := os.Stat(configPath(dir)); !os.IsNotExist(err) {
			t.Fatal("invalid patch created file")
		}
	}
}
func TestReadMissingAndConcurrentPatch(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	if _, err := ReadConfig(dir, cfg); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing config not distinguishable")
	}
	writeNativeFixture(t, dir, nativeConfigFixture())
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := DefaultConfig()
			c.RequestRetry = 4
			if err := c.PatchConfig(dir, []string{"request_retry"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := ReadConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fileBytes(t, configPath(dir))), "native-alias") {
		t.Fatal("concurrent patch lost native fields")
	}
}
func TestConfigSymlinkAndBackupSymlinkAreRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink requires privilege")
	}
	for _, name := range []string{"config.yaml", ConfigBackupName} {
		dir := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.yaml")
		data := nativeConfigFixture()
		if err := os.WriteFile(outside, data, 0600); err != nil {
			t.Fatal(err)
		}
		if name == ConfigBackupName {
			writeNativeFixture(t, dir, data)
		}
		if err := os.Symlink(outside, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		cfg := DefaultConfig()
		if err := cfg.PatchConfig(dir, []string{"debug"}); err == nil {
			t.Fatal("unsafe config path accepted")
		}
		if !bytes.Equal(data, fileBytes(t, outside)) {
			t.Fatal("symlink target overwritten")
		}
	}
}
