package store

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// APIKey 缓存
var (
	apiKeyCacheMu sync.RWMutex
	apiKeyCache   = make(map[string]*cachedAPIKey)
)

type cachedAPIKey struct {
	key    *APIKey
	expiry time.Time
}

// InvalidateAPIKeyCache 清除 API Key 缓存（创建/删除密钥时调用）
func (r *APIKeyRepo) InvalidateAPIKeyCache() {
	apiKeyCacheMu.Lock()
	defer apiKeyCacheMu.Unlock()
	apiKeyCache = make(map[string]*cachedAPIKey)
}

// APIKey API 密钥
type APIKey struct {
	ID            int64     `json:"id"`
	Key           string    `json:"key"`
	Name          string    `json:"name"`
	Enabled       bool      `json:"enabled"`
	// 额度管理（余额制）
	QuotaEnabled  bool      `json:"quota_enabled"`  // 是否启用额度限制
	QuotaBalance  float64   `json:"quota_balance"`  // 剩余额度（$）
	QuotaUsed     float64   `json:"quota_used"`     // 累计已用（$）
	AllowedModels string    `json:"allowed_models"` // 允许的模型列表 JSON 数组，空=全部允许
	CreatedAt     time.Time `json:"created_at"`
}

type APIKeyRepo struct {
	db *DB
}

func NewAPIKeyRepo(db *DB) *APIKeyRepo {
	return &APIKeyRepo{db: db}
}

// keyColumns 常用查询列
const keyColumns = `id, key, name, enabled, quota_enabled, quota_balance, quota_used, allowed_models, created_at`

func (r *APIKeyRepo) List() ([]APIKey, error) {
	rows, err := r.db.Query(`SELECT ` + keyColumns + ` FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Key, &k.Name, &k.Enabled, &k.QuotaEnabled, &k.QuotaBalance, &k.QuotaUsed, &k.AllowedModels, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func (r *APIKeyRepo) GetByKey(key string) (*APIKey, error) {
	// 先查缓存
	apiKeyCacheMu.RLock()
	if cached, ok := apiKeyCache[key]; ok && time.Now().Before(cached.expiry) {
		apiKeyCacheMu.RUnlock()
		k := *cached.key // 返回副本
		return &k, nil
	}
	apiKeyCacheMu.RUnlock()

	k := &APIKey{}
	err := r.db.QueryRow(
		`SELECT `+keyColumns+` FROM api_keys WHERE key = ? AND enabled = 1`, key,
	).Scan(&k.ID, &k.Key, &k.Name, &k.Enabled, &k.QuotaEnabled, &k.QuotaBalance, &k.QuotaUsed, &k.AllowedModels, &k.CreatedAt)
	if err != nil {
		return nil, err
	}

	// 写入缓存（5 分钟 TTL）
	apiKeyCacheMu.Lock()
	apiKeyCache[key] = &cachedAPIKey{
		key:    k,
		expiry: time.Now().Add(5 * time.Minute),
	}
	apiKeyCacheMu.Unlock()

	return k, nil
}

func (r *APIKeyRepo) Create(name string) (*APIKey, error) {
	key, err := generateAPIKey()
	if err != nil {
		return nil, err
	}
	result, err := r.db.Exec(`INSERT INTO api_keys (key, name, enabled) VALUES (?, ?, 1)`, key, name)
	if err != nil {
		return nil, err
	}
	id, _ := result.LastInsertId()
	return r.getByID(id)
}

func (r *APIKeyRepo) getByID(id int64) (*APIKey, error) {
	k := &APIKey{}
	err := r.db.QueryRow(
		`SELECT `+keyColumns+` FROM api_keys WHERE id = ?`, id,
	).Scan(&k.ID, &k.Key, &k.Name, &k.Enabled, &k.QuotaEnabled, &k.QuotaBalance, &k.QuotaUsed, &k.AllowedModels, &k.CreatedAt)
	if err != nil {
		return nil, err
	}
	return k, nil
}

func (r *APIKeyRepo) Toggle(id int64) error {
	_, err := r.db.Exec(`UPDATE api_keys SET enabled = NOT enabled WHERE id = ?`, id)
	return err
}

func (r *APIKeyRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	return err
}

// UpdateConfig 更新 API Key 的额度与模型配置
// 返回更新后的密钥
func (r *APIKeyRepo) UpdateConfig(id int64, quotaEnabled bool, quotaBalance float64, allowedModels string) (*APIKey, error) {
	_, err := r.db.Exec(
		`UPDATE api_keys SET quota_enabled = ?, quota_balance = ?, allowed_models = ? WHERE id = ?`,
		quotaEnabled, quotaBalance, allowedModels, id,
	)
	if err != nil {
		return nil, err
	}
	r.InvalidateAPIKeyCache()
	return r.getByID(id)
}

// DeductQuota 扣减 API Key 额度（按实际用量 cost）
// 原子操作：balance 减 cost，used 加 cost
// 返回扣减后的余额；如果扣减后为负则限制为 0（不会负余额）
func (r *APIKeyRepo) DeductQuota(id int64, cost float64) (float64, error) {
	if cost <= 0 {
		return 0, nil
	}
	// 原子扣减，限制余额不低于 0
	_, err := r.db.Exec(
		`UPDATE api_keys SET
			quota_balance = MAX(0, quota_balance - ?),
			quota_used = quota_used + ?
		 WHERE id = ? AND quota_enabled = 1`,
		cost, cost, id,
	)
	if err != nil {
		return 0, err
	}
	// 读取最新余额
	var balance float64
	err = r.db.QueryRow(`SELECT quota_balance FROM api_keys WHERE id = ?`, id).Scan(&balance)
	if err != nil {
		return 0, err
	}
	return balance, nil
}

// generateAPIKey 生成随机 API Key（sk- 前缀 + 48 位十六进制）
func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sk-" + hex.EncodeToString(b), nil
}
