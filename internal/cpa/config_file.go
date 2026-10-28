package cpa

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

const ConfigBackupName = "config.yaml.zero-api.bak"
const maxConfigBytes = 8 << 20

var (
	configFileMu          sync.RWMutex
	ErrInvalidConfig      = errors.New("CPA 原生配置无效，已保留原文件，请先修复 YAML")
	ErrConfigChanged      = errors.New("CPA 配置在保存期间发生变化，请刷新后重试")
	ErrUnsafeConfig       = errors.New("CPA 配置必须是可访问的普通文件")
	ErrConfigWrite        = errors.New("无法安全保存 CPA 配置，原生设置未被重置")
	ErrInvalidConfigField = errors.New("CPA 配置字段或字段值无效")
)

func configPath(dataDir string) string { return filepath.Join(dataDir, "config.yaml") }

func readConfigFile(path string) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, ErrUnsafeConfig
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrUnsafeConfig
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) || info.Size() > maxConfigBytes {
		return nil, ErrUnsafeConfig
	}
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil || len(data) > maxConfigBytes {
		return nil, ErrUnsafeConfig
	}
	return data, nil
}

func parseConfig(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if decoder.Decode(&doc) != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, ErrInvalidConfig
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, ErrInvalidConfig
	}
	// Decode once to reject duplicate keys and malformed aliases; never expose
	// decoder errors because they may contain provider credentials or snippets.
	var check any
	if doc.Decode(&check) != nil {
		return nil, ErrInvalidConfig
	}
	return &doc, nil
}

func mappingValue(parent *yaml.Node, key string) *yaml.Node {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

func sameNode(a, b *yaml.Node) bool {
	var av, bv any
	if a.Decode(&av) != nil || b.Decode(&bv) != nil {
		return false
	}
	// Canonical encoding compares values rather than YAML presentation/comments.
	x, err := yaml.Marshal(av)
	if err != nil {
		return false
	}
	y, err := yaml.Marshal(bv)
	return err == nil && bytes.Equal(x, y)
}

func setMappingValue(parent *yaml.Node, key string, value any) (bool, error) {
	if parent.Kind != yaml.MappingNode {
		return false, ErrInvalidConfigField
	}
	var node yaml.Node
	if node.Encode(value) != nil {
		return false, ErrInvalidConfigField
	}
	if old := mappingValue(parent, key); old != nil {
		if sameNode(old, &node) {
			return false, nil
		}
		// Retain anchors and comments so aliases to an explicitly edited scalar or
		// sequence remain well formed. Native-only nodes are never reconstructed.
		node.HeadComment, node.LineComment, node.FootComment, node.Anchor = old.HeadComment, old.LineComment, old.FootComment, old.Anchor
		*old = node
		return true, nil
	}
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &node)
	return true, nil
}

// ReadConfig overlays only file-present, shared settings onto fallback. Lifecycle
// switches and the raw management key remain zero-api database state. The native
// secret-key (including CPA's bcrypt hash) and auth-dir are never changed here.
func ReadConfig(dataDir string, fallback *Config) (*Config, error) {
	configFileMu.RLock()
	defer configFileMu.RUnlock()
	data, err := readConfigFile(configPath(dataDir))
	if err != nil {
		return nil, err
	}
	doc, err := parseConfig(data)
	if err != nil {
		return nil, err
	}
	var result Config
	if fallback == nil {
		result = *DefaultConfig()
	} else {
		result = *fallback
		result.APIKeys = append([]string{}, fallback.APIKeys...)
	}
	var values map[string]any
	if doc.Content[0].Decode(&values) != nil {
		return nil, ErrInvalidConfig
	}
	decode := func(value any, target any) error {
		var node yaml.Node
		if node.Encode(value) != nil || node.Decode(target) != nil {
			return ErrInvalidConfig
		}
		return nil
	}
	fields := []struct {
		key    string
		target any
	}{
		{"host", &result.Host}, {"port", &result.Port}, {"api-keys", &result.APIKeys},
		{"proxy-url", &result.ProxyURL}, {"request-retry", &result.RequestRetry}, {"debug", &result.Debug},
	}
	for _, field := range fields {
		if value, present := values[field.key]; present {
			if err := decode(value, field.target); err != nil {
				return nil, err
			}
		}
	}
	if value, present := values["remote-management"]; present {
		management, ok := value.(map[string]any)
		if !ok {
			return nil, ErrInvalidConfig
		}
		if value, present := management["allow-remote"]; present {
			if err := decode(value, &result.AllowRemote); err != nil {
				return nil, err
			}
		}
	}
	if result.APIKeys == nil {
		result.APIKeys = []string{}
	}
	if result.Port < 1 || result.Port > 65535 || result.RequestRetry < 0 {
		return nil, ErrInvalidConfig
	}
	return &result, nil
}

func (c *Config) initialConfig(dataDir string) ([]byte, error) {
	if c.Port < 1 || c.Port > 65535 || c.RequestRetry < 0 {
		return nil, ErrInvalidConfigField
	}
	data, err := c.Render()
	if err != nil {
		return nil, ErrConfigWrite
	}
	doc, err := parseConfig(data)
	if err != nil {
		return nil, err
	}
	authDir, err := filepath.Abs(filepath.Join(dataDir, "auths"))
	if err != nil {
		return nil, ErrConfigWrite
	}
	if _, err = setMappingValue(doc.Content[0], "auth-dir", authDir); err != nil {
		return nil, err
	}
	data, err = yaml.Marshal(doc)
	if err != nil {
		return nil, ErrConfigWrite
	}
	return data, nil
}

// WriteConfig is an ensure operation: restart/upgrade/auth never regenerate an
// existing native config. Existing files are validated and left byte-for-byte intact.
func (c *Config) WriteConfig(dataDir string) error {
	configFileMu.Lock()
	defer configFileMu.Unlock()
	return c.ensureConfig(dataDir)
}

func (c *Config) ensureConfig(dataDir string) error {
	path := configPath(dataDir)
	data, err := readConfigFile(path)
	if err == nil {
		_, err = parseConfig(data)
		return err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = os.MkdirAll(dataDir, 0700); err != nil {
		return ErrConfigWrite
	}
	data, err = c.initialConfig(dataDir)
	if err != nil {
		return err
	}
	// O_EXCL prevents a native config created concurrently from being overwritten.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		data, err = readConfigFile(path)
		if err == nil {
			_, err = parseConfig(data)
		}
		return err
	}
	if err != nil {
		return ErrConfigWrite
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return ErrConfigWrite
	}
	return nil
}

// Expand only an edited management alias/merge locally; do not mutate its
// native template or lose inherited secret-key/unknown management settings.
func configManagementMapping(root *yaml.Node) (*yaml.Node, error) {
	old := mappingValue(root, "remote-management")
	if old != nil && old.Kind == yaml.MappingNode {
		return old, nil
	}
	if old != nil && old.Kind != yaml.AliasNode {
		return nil, ErrInvalidConfig
	}
	var values map[string]any
	if root.Decode(&values) != nil {
		return nil, ErrInvalidConfig
	}
	var node yaml.Node
	value, present := values["remote-management"]
	if !present {
		value = map[string]any{}
	}
	if node.Encode(value) != nil || node.Kind != yaml.MappingNode {
		return nil, ErrInvalidConfig
	}
	if old != nil {
		node.HeadComment, node.LineComment, node.FootComment = old.HeadComment, old.LineComment, old.FootComment
		*old = node
		return old, nil
	}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "remote-management"}, &node)
	return &node, nil
}

func patchConfigNode(doc *yaml.Node, c *Config, fields []string) (bool, error) {
	changed := false
	for _, field := range fields {
		parent := doc.Content[0]
		var key string
		var value any
		switch field {
		case "host":
			key, value = "host", c.Host
		case "port":
			if c.Port < 1 || c.Port > 65535 {
				return false, ErrInvalidConfigField
			}
			key, value = "port", c.Port
		case "api_keys":
			key, value = "api-keys", c.APIKeys
			if c.APIKeys == nil {
				value = []string{}
			}
		case "proxy_url":
			key, value = "proxy-url", c.ProxyURL
		case "request_retry":
			if c.RequestRetry < 0 {
				return false, ErrInvalidConfigField
			}
			key, value = "request-retry", c.RequestRetry
		case "debug":
			key, value = "debug", c.Debug
		case "allow_remote":
			key, value = "allow-remote", c.AllowRemote
			var err error
			parent, err = configManagementMapping(parent)
			if err != nil {
				return false, err
			}

		default:
			return false, ErrInvalidConfigField
		}
		modified, err := setMappingValue(parent, key, value)
		if err != nil {
			return false, err
		}
		changed = changed || modified
	}
	return changed, nil
}

func writeConfigTemp(dataDir string, data []byte) (string, error) {
	file, err := os.CreateTemp(dataDir, ".zero-api-config-*.tmp")
	if err != nil {
		return "", ErrConfigWrite
	}
	path := file.Name()
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(path)
		return "", ErrConfigWrite
	}
	return path, nil
}

func regularOrMissing(path string) bool {
	info, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist) || err == nil && info.Mode().IsRegular()
}

// PatchConfig only changes explicit shared fields. It preserves native settings,
// validates the resulting YAML, backs up the complete prior file privately, then
// uses same-directory atomic rename. Cross-process changes are checked before
// replacement; callers must refresh/retry on a detected native-panel race.
func (c *Config) PatchConfig(dataDir string, fields []string) error {
	configFileMu.Lock()
	defer configFileMu.Unlock()
	if len(fields) == 0 {
		return nil
	}
	// Validate field names/values before any file creation or backup side effect.
	validation := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	if _, err := patchConfigNode(validation, c, fields); err != nil {
		return err
	}
	if err := c.ensureConfig(dataDir); err != nil {
		return err
	}
	path := configPath(dataDir)
	original, err := readConfigFile(path)
	if err != nil {
		return err
	}
	doc, err := parseConfig(original)
	if err != nil {
		return err
	}
	changed, err := patchConfigNode(doc, c, fields)
	if err != nil || !changed {
		return err
	}
	updated, err := yaml.Marshal(doc)
	if err != nil {
		return ErrConfigWrite
	}
	if _, err = parseConfig(updated); err != nil {
		return err
	}
	temp, err := writeConfigTemp(dataDir, updated)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	backupTemp, err := writeConfigTemp(dataDir, original)
	if err != nil {
		return err
	}
	defer os.Remove(backupTemp)
	backup := filepath.Join(dataDir, ConfigBackupName)
	if !regularOrMissing(backup) {
		return ErrUnsafeConfig
	}
	current, err := readConfigFile(path)
	if err != nil || !bytes.Equal(current, original) {
		return ErrConfigChanged
	}
	if err = os.Rename(backupTemp, backup); err != nil {
		return ErrConfigWrite
	}
	// Recheck after writing the backup, immediately before replacing the live file.
	current, err = readConfigFile(path)
	if err != nil || !bytes.Equal(current, original) {
		return ErrConfigChanged
	}
	if err = os.Rename(temp, path); err != nil {
		return ErrConfigWrite
	}
	return nil
}
