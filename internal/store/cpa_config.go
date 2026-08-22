package store

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// CPAConfig CLIProxyAPI sidecar 配置（存储在 DB）
type CPAConfig struct {
	ID            int64    `json:"id"`
	Enabled       bool     `json:"enabled"`
	AutoStart     bool     `json:"auto_start"`
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	APIKeys       []string `json:"api_keys"`
	ProxyURL      string   `json:"proxy_url,omitempty"`
	RequestRetry  int      `json:"request_retry"`
	Debug         bool     `json:"debug"`
	ManagementKey string   `json:"-"` // CLIProxyAPI Management API 密钥，不返回前端
	// 数据目录（不可通过 API 修改）
	DataDir   string    `json:"data_dir"`
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

// EnsureManagementKey 返回持久化的 CLIProxyAPI Management API 密钥。
// 首次调用时生成高熵随机密钥，后续调用保持不变。
func (r *CPAConfigRepo) EnsureManagementKey() (string, error) {
	var key string
	if err := r.db.QueryRow(`SELECT management_key FROM cpa_config WHERE id=1`).Scan(&key); err != nil {
		return "", err
	}
	if key != "" {
		return key, nil
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("生成 CLIProxyAPI Management Key 失败: %w", err)
	}
	key = base64.RawURLEncoding.EncodeToString(raw)
	if _, err := r.db.Exec(`UPDATE cpa_config SET management_key=?, updated_at=CURRENT_TIMESTAMP WHERE id=1`, key); err != nil {
		return "", fmt.Errorf("保存 CLIProxyAPI Management Key 失败: %w", err)
	}
	return key, nil
}

// Get 获取配置（单行）
func (r *CPAConfigRepo) Get() (*CPAConfig, error) {
	var c CPAConfig
	var apiKeysJSON, createdAt, updatedAt string
	err := r.db.QueryRow(
		`SELECT id, enabled, auto_start, host, port, api_keys, proxy_url, request_retry, debug,
		        management_key, data_dir, created_at, updated_at
		 FROM cpa_config LIMIT 1`,
	).Scan(&c.ID, &c.Enabled, &c.AutoStart, &c.Host, &c.Port, &apiKeysJSON,
		&c.ProxyURL, &c.RequestRetry, &c.Debug, &c.ManagementKey,
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
		        request_retry=?, debug=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=1`,
		boolToInt(c.Enabled), boolToInt(c.AutoStart), c.Host, c.Port, string(apiKeysJSON),
		c.ProxyURL, c.RequestRetry, boolToInt(c.Debug),
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
		`INSERT INTO cpa_config (enabled, auto_start, host, port, api_keys, request_retry, debug, data_dir)
		 VALUES (1, 1, '127.0.0.1', 8317, '[]', 3, 0, ?)`, dataDir)
	return err
}
