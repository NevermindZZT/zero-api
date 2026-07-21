package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// PricingRule 配置层定价规则定义（与 pricing.PricingRule 一致，避免循环依赖）
type PricingRule struct {
	ID      string `yaml:"id"`
	Type    string `yaml:"type"`
	Enabled bool   `yaml:"enabled"`
	Name    string `yaml:"name"`

	Days      []string `yaml:"days,omitempty"`
	StartTime string   `yaml:"start_time,omitempty"`
	EndTime   string   `yaml:"end_time,omitempty"`

	PromptMaxTokens  int `yaml:"prompt_max_tokens,omitempty"`
	ContextMaxTokens int `yaml:"context_max_tokens,omitempty"`

	PricingInput      float64 `yaml:"pricing_input"`
	PricingOutput     float64 `yaml:"pricing_output"`
	PricingCacheRead  float64 `yaml:"pricing_cache_read"`
	PricingCacheWrite float64 `yaml:"pricing_cache_write"`
}

// ModelDefault 预填的模型默认数据
// 优先级：内置 modelDB < 配置默认值 < 上游 API < 用户手动编辑
type ModelDefault struct {
	ContextWindow   int     `yaml:"context_window" json:"context_window"`
	MaxOutputTokens int     `yaml:"max_output_tokens" json:"max_output_tokens"`
	SupportsVision  bool    `yaml:"supports_vision" json:"supports_vision"`
	SupportsThinking bool   `yaml:"supports_thinking" json:"supports_thinking"`
	SupportsTools   bool    `yaml:"supports_tools" json:"supports_tools"`
	PricingInput      float64 `yaml:"pricing_input" json:"pricing_input"`
	PricingOutput     float64 `yaml:"pricing_output" json:"pricing_output"`
	PricingCacheRead  float64 `yaml:"pricing_cache_read" json:"pricing_cache_read"`
	PricingCacheWrite float64 `yaml:"pricing_cache_write" json:"pricing_cache_write"`
	PricingRules      []PricingRule `yaml:"pricing_rules,omitempty" json:"pricing_rules,omitempty"`
}

type Config struct {
	Server        ServerConfig           `yaml:"server"`
	Proxy         ProxyConfig            `yaml:"proxy"`
	Upstream      UpstreamConfig         `yaml:"upstream"`
	Database      DatabaseConfig          `yaml:"database"`
	Auth          AuthConfig              `yaml:"auth"`
	MCP           MCPConfig              `yaml:"mcp"`
	ModelDefaults map[string]ModelDefault `yaml:"model_defaults"`
	LogLevel      string                  `yaml:"log_level"`
}

// ===== 模型预设文件管理 =====

// ModelPresets 模型预设数据（从 JSON 文件热加载）
type ModelPresets struct {
	mu      sync.RWMutex
	presets map[string]ModelDefault
}

// NewModelPresets 加载模型预设文件
func NewModelPresets(presetsPath string, yamlDefaults map[string]ModelDefault) *ModelPresets {
	mp := &ModelPresets{}
	mp.load(presetsPath, yamlDefaults)
	return mp
}

// load 加载预设（优先 JSON 文件，不存在时从 yaml 迁移创建）
func (mp *ModelPresets) load(presetsPath string, yamlDefaults map[string]ModelDefault) {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	mp.presets = make(map[string]ModelDefault)

	if data, err := os.ReadFile(presetsPath); err == nil {
		if json.Unmarshal(data, &mp.presets) == nil && len(mp.presets) > 0 {
			log.Printf("[预设] 从 %s 加载 %d 个模型预设", presetsPath, len(mp.presets))
			return
		}
	}

	// JSON 文件不存在或为空 → 从 config.yaml model_defaults 迁移
	if len(yamlDefaults) > 0 {
		mp.presets = yamlDefaults
		mp.save(presetsPath)
		log.Printf("[预设] 从 config.yaml 迁移 %d 个模型默认值到 %s", len(yamlDefaults), presetsPath)
		return
	}

	log.Printf("[预设] 未找到模型预设文件，定价等功能可能不正常")
}

// save 写回 JSON 文件
func (mp *ModelPresets) save(presetsPath string) {
	data, err := json.MarshalIndent(mp.presets, "", "  ")
	if err != nil {
		log.Printf("[预设] 序列化预设失败: %v", err)
		return
	}
	os.MkdirAll(filepath.Dir(presetsPath), 0755)
	if err := os.WriteFile(presetsPath, data, 0644); err != nil {
		log.Printf("[预设] 写入 %s 失败: %v", presetsPath, err)
	}
}

// GetAll 返回当前预设
func (mp *ModelPresets) GetAll() map[string]ModelDefault {
	mp.mu.RLock()
	defer mp.mu.RUnlock()
	cp := make(map[string]ModelDefault, len(mp.presets))
	for k, v := range mp.presets {
		cp[k] = v
	}
	return cp
}

// Reload 重新加载预设文件
func (mp *ModelPresets) Reload(presetsPath string) error {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	data, err := os.ReadFile(presetsPath)
	if err != nil {
		return fmt.Errorf("读取预设文件失败: %w", err)
	}
	mp.presets = make(map[string]ModelDefault)
	if err := json.Unmarshal(data, &mp.presets); err != nil {
		return fmt.Errorf("解析预设文件失败: %w", err)
	}
	log.Printf("[预设] 重新加载完成，共 %d 个模型", len(mp.presets))
	return nil
}

// Merge 合并预设（用于 API 更新后写回文件）
func (mp *ModelPresets) Merge(presetsPath string, items map[string]ModelDefault) error {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	for k, v := range items {
		mp.presets[k] = v
	}
	mp.save(presetsPath)
	return nil
}

type AuthConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Secret   string `yaml:"secret"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

func (s ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

type ProxyConfig struct {
	Enabled               bool     `yaml:"enabled"`
	Host                  string   `yaml:"host"`
	Port                  int      `yaml:"port"`
	InterceptDomains      []string `yaml:"intercept_domains"`
	SmartInterceptDomains []string `yaml:"smart_intercept_domains"`
}

type UpstreamConfig struct {
	RequestTimeoutSeconds int `yaml:"request_timeout_seconds"`
}

type MCPConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Token     string `yaml:"token"`
	SkillsDir string `yaml:"skills_dir"`
}

func (p ProxyConfig) Addr() string {
	return fmt.Sprintf("%s:%d", p.Host, p.Port)
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

// Load 从指定路径加载配置文件
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 应用默认值
	cfg.applyDefaults()
	return cfg, nil
}

// LoadDefault 加载默认路径的配置文件
func LoadDefault() (*Config, error) {
	// 依次尝试多个路径
	paths := []string{
		"configs/config.yaml",
		"config.yaml",
		"/etc/zero-api/config.yaml",
	}

	// 支持环境变量覆盖
	if envPath := os.Getenv("ZERO_API_CONFIG"); envPath != "" {
		paths = append([]string{envPath}, paths...)
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			abs, _ := filepath.Abs(p)
			return Load(abs)
		}
	}

	return nil, fmt.Errorf("未找到配置文件，尝试路径: %v", paths)
}

func (c *Config) applyDefaults() {
	if c.Server.Host == "" {
		c.Server.Host = "0.0.0.0"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Proxy.Host == "" {
		c.Proxy.Host = "0.0.0.0"
	}
	if c.Proxy.Port == 0 {
		c.Proxy.Port = 8520
	}
	if c.Upstream.RequestTimeoutSeconds == 0 {
		c.Upstream.RequestTimeoutSeconds = 60
	}
	if c.Database.Path == "" {
		c.Database.Path = "data/zero-api.db"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.Auth.Username == "" {
		c.Auth.Username = "admin"
	}
	if c.Auth.Password == "" {
		c.Auth.Password = "admin123"
	}
	if c.Auth.Secret == "" {
		c.Auth.Secret = "zero-api-default-secret"
	}
	if !c.MCP.Enabled {
		c.MCP.Enabled = true
	}
	if c.MCP.SkillsDir == "" {
		c.MCP.SkillsDir = "data/skills"
	}
}
