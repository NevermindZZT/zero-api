package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ModelDefault 预填的模型默认数据
// 优先级：内置 modelDB < 配置默认值 < 上游 API < 用户手动编辑
type ModelDefault struct {
	ContextWindow   int     `yaml:"context_window"`
	MaxOutputTokens int     `yaml:"max_output_tokens"`
	SupportsVision  bool    `yaml:"supports_vision"`
	SupportsThinking bool   `yaml:"supports_thinking"`
	SupportsTools   bool    `yaml:"supports_tools"`
	PricingInput      float64 `yaml:"pricing_input"`
	PricingOutput     float64 `yaml:"pricing_output"`
	PricingCacheRead  float64 `yaml:"pricing_cache_read"`
	PricingCacheWrite float64 `yaml:"pricing_cache_write"`
}

type Config struct {
	Server        ServerConfig           `yaml:"server"`
	Proxy         ProxyConfig            `yaml:"proxy"`
	Database      DatabaseConfig          `yaml:"database"`
	Auth          AuthConfig              `yaml:"auth"`
	ModelDefaults map[string]ModelDefault `yaml:"model_defaults"`
	LogLevel      string                  `yaml:"log_level"`
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
}
