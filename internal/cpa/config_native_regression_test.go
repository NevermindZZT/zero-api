package cpa

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestNativeYAMLMergesAndManagementAliasesArePreserved(t *testing.T) {
	dir := t.TempDir()
	original := []byte(`defaults: &base
  host: 0.0.0.0
  port: 9317
  debug: true
  remote-management: &management
    allow-remote: true
    secret-key: native-secret
    panel-github-repository: https://example.invalid/panel
<<: *base
remote-management: *management
`)
	writeNativeFixture(t, dir, original)
	cfg := DefaultConfig()
	effective, err := ReadConfig(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if effective.Port != 9317 || !effective.Debug || !effective.AllowRemote {
		t.Fatal("merged native values not reflected")
	}
	cfg.AllowRemote = false
	cfg.Debug = false
	if err := cfg.PatchConfig(dir, []string{"allow_remote", "debug"}); err != nil {
		t.Fatal(err)
	}
	root := configMap(t, fileBytes(t, configPath(dir)))
	management := root["remote-management"].(map[string]any)
	if management["allow-remote"] != false || management["secret-key"] != "native-secret" || management["panel-github-repository"] != "https://example.invalid/panel" {
		t.Fatal("aliased management fields lost")
	}
	if root["defaults"].(map[string]any)["debug"] != true || root["debug"] != false {
		t.Fatal("shared native template unexpectedly mutated")
	}
}

func TestManagerSettingsRefreshUsesSafeSnapshots(t *testing.T) {
	manager := NewManager(filepath.Join(t.TempDir(), "cpa"), "127.0.0.1", 8317)
	var group sync.WaitGroup
	for i := 0; i < 6; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for n := 0; n < 30; n++ {
				manager.UpdateEndpoint("", 9317)
				manager.SetProxyURL("http://test.invalid:7890")
				_ = manager.proxyURLSnapshot()
				state := manager.Status()
				if state["port"] != 9317 || state["host"] != "" {
					t.Error("explicit native endpoint not reflected")
				}
			}
		}()
	}
	group.Wait()
}
