package cpa

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config CLIProxyAPI 配置（管理面板可编辑字段的映射）
type Config struct {
	// Enabled 是否启用 sidecar（启动时自动拉起）
	Enabled bool `json:"enabled"`
	// AutoStart 是否随 zero-api 启动自动启动
	AutoStart bool `json:"auto_start"`
	// Host 绑定地址
	Host string `json:"host"`
	// Port 监听端口
	Port int `json:"port"`
	// APIKeys 访问 CLIProxyAPI 的 API Key 列表（zero-api 渠道用它做认证）
	APIKeys []string `json:"api_keys"`
	// ProxyURL 出站代理（可选，用于访问 ChatGPT 后端）
	ProxyURL string `json:"proxy_url,omitempty"`
	// RequestRetry 请求重试次数
	RequestRetry int `json:"request_retry"`
	// Debug 调试模式
	Debug bool `json:"debug"`

	// 订阅渠道可用性由 auths 目录中的认证文件决定，无需开关配置。
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Enabled:      true,
		AutoStart:    true,
		Host:         "127.0.0.1",
		Port:         8317,
		APIKeys:      []string{},
		RequestRetry: 3,
	}
}

// Render 生成 CLIProxyAPI config.yaml 内容
func (c *Config) Render() ([]byte, error) {
	type cpaYAML struct {
		Host    string   `yaml:"host"`
		Port    int      `yaml:"port"`
		AuthDir string   `yaml:"auth-dir"`
		APIKeys []string `yaml:"api-keys"`
		Debug   bool     `yaml:"debug"`

		ProxyURL     string `yaml:"proxy-url,omitempty"`
		RequestRetry int    `yaml:"request-retry"`

		Codex struct {
			DisableCodexCloaking bool `yaml:"disable-codex-cloaking"`
		} `yaml:"codex,omitempty"`

		// OAuth 模型前缀/别名
		OAuthModelAlias map[string][]oauthAlias `yaml:"oauth-model-alias,omitempty"`
		OAuthExcluded   map[string][]string     `yaml:"oauth-excluded-models,omitempty"`
	}

	var y cpaYAML
	y.Host = c.Host
	y.Port = c.Port
	y.AuthDir = "auths" // 相对 config 文件所在目录（CLIProxyAPI 相对 auth-dir 解释）
	y.APIKeys = c.APIKeys
	y.Debug = c.Debug
	if c.ProxyURL != "" {
		y.ProxyURL = c.ProxyURL
	}
	if c.RequestRetry > 0 {
		y.RequestRetry = c.RequestRetry
	}

	out, err := yaml.Marshal(&y)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// oauthAlias OAuth 模型别名
type oauthAlias struct {
	Name        string `yaml:"name"`
	Alias       string `yaml:"alias"`
	DisplayName string `yaml:"display-name,omitempty"`
}

// WriteConfig 将配置写入文件
func (c *Config) WriteConfig(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	content, err := c.Render()
	if err != nil {
		return fmt.Errorf("生成配置失败: %w", err)
	}
	// 简化：auth-dir 用绝对路径（CLIProxyAPI 相对 config 文件目录解析相对路径）
	// 直接写绝对路径更稳妥
	absAuth := filepath.Join(dataDir, "auths")
	content = []byte(strings.Replace(string(content), `auth-dir: auths`, `auth-dir: `+absAuth, 1))
	if err := os.WriteFile(filepath.Join(dataDir, "config.yaml"), content, 0644); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	return nil
}
