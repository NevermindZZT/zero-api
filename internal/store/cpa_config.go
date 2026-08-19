package store

import (
	"encoding/json"
	"time"
)

// CPAConfig CLIProxyAPI sidecar 配置（存储在 DB）
type CPAConfig struct {
	ID       int64     `json:"id"`
	Enabled  bool      `json:"enabled"`
	AutoStart bool     `json:"auto_start"`
	Host     string    `json:"host"`
	Port     int       `json:"port"`
	APIKeys  []string  `json:"api_keys"`
	ProxyURL string    `json:"proxy_url,omitempty"`
	RequestRetry int   `json:"request_retry"`
	Debug    bool      `json:"debug"`
	// 订阅接入开关
	EnableCodex   bool   `json:"enable_codex"`
	CodexPrefix   string `json:"codex_prefix,omitempty"`
	EnableClaude  bool   `json:"enable_claude"`
	EnableGemini  bool   `json:"enable_gemini"`
	EnableGrok    bool   `json:"enable_grok"`
	EnableAntigravity bool `json:"enable_antigravity"`
	// 数据目录（不可通过 API 修改）
	DataDir string    `json:"data_dir"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CPAConfigRepo CLIProxyAPI 配置存储
type CPAConfigRepo struct {
	db *DB
}

func NewCPAConfigRepo(db *DB) *CPAConfigRepo {
	return &CPAConfigRepo{db: db}
}

// Get 获取配置（单行）
func (r *CPAConfigRepo) Get() (*CPAConfig, error) {
	var c CPAConfig
	var apiKeysJSON, createdAt, updatedAt string
	err := r.db.QueryRow(
		`SELECT id, enabled, auto_start, host, port, api_keys, proxy_url, request_retry, debug,
		        enable_codex, codex_prefix, enable_claude, enable_gemini, enable_grok, enable_antigravity,
		        data_dir, created_at, updated_at
		 FROM cpa_config LIMIT 1`,
	).Scan(&c.ID, &c.Enabled, &c.AutoStart, &c.Host, &c.Port, &apiKeysJSON,
		&c.ProxyURL, &c.RequestRetry, &c.Debug,
		&c.EnableCodex, &c.CodexPrefix, &c.EnableClaude, &c.EnableGemini, &c.EnableGrok, &c.EnableAntigravity,
		&c.DataDir, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(apiKeysJSON), &c.APIKeys)
	c.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	c.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
	if c.APIKeys == nil {
		c.APIKeys = []string{}
	}
	return &c, nil
}

// Save 保存配置（upsert 单行）
func (r *CPAConfigRepo) Save(c *CPAConfig) error {
	apiKeysJSON, _ := json.Marshal(c.APIKeys)
	_, err := r.db.Exec(
		`UPDATE cpa_config SET enabled=?, auto_start=?, host=?, port=?, api_keys=?, proxy_url=?,
		        request_retry=?, debug=?, enable_codex=?, codex_prefix=?, enable_claude=?,
		        enable_gemini=?, enable_grok=?, enable_antigravity=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=1`,
		boolToInt(c.Enabled), boolToInt(c.AutoStart), c.Host, c.Port, string(apiKeysJSON),
		c.ProxyURL, c.RequestRetry, boolToInt(c.Debug),
		boolToInt(c.EnableCodex), c.CodexPrefix, boolToInt(c.EnableClaude),
		boolToInt(c.EnableGemini), boolToInt(c.EnableGrok), boolToInt(c.EnableAntigravity),
	)
	if err != nil {
		return err
	}
	// 更新 data_dir 如有变化（首次插入时）
	if c.DataDir != "" {
		r.db.Exec(`UPDATE cpa_config SET data_dir=? WHERE id=1`, c.DataDir)
	}
	return nil
}

// boolToInt bool → 0/1
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Init 初始化默认配置行（无记录时插入）
func (r *CPAConfigRepo) Init(dataDir string) error {
	var count int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM cpa_config`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := r.db.Exec(
		`INSERT INTO cpa_config (enabled, auto_start, host, port, api_keys, request_retry, debug,
		        enable_codex, enable_claude, enable_gemini, enable_grok, enable_antigravity, data_dir)
		 VALUES (1, 1, '127.0.0.1', 8317, '[]', 3, 0, 1, 0, 0, 0, 0, ?)`, dataDir)
	return err
}
