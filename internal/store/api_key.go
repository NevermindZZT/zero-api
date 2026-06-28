package store

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// APIKey API 密钥
type APIKey struct {
	ID        int64     `json:"id"`
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type APIKeyRepo struct {
	db *DB
}

func NewAPIKeyRepo(db *DB) *APIKeyRepo {
	return &APIKeyRepo{db: db}
}

func (r *APIKeyRepo) List() ([]APIKey, error) {
	rows, err := r.db.Query(`SELECT id, key, name, enabled, created_at FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Key, &k.Name, &k.Enabled, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func (r *APIKeyRepo) GetByKey(key string) (*APIKey, error) {
	k := &APIKey{}
	err := r.db.QueryRow(
		`SELECT id, key, name, enabled, created_at FROM api_keys WHERE key = ? AND enabled = 1`, key,
	).Scan(&k.ID, &k.Key, &k.Name, &k.Enabled, &k.CreatedAt)
	if err != nil {
		return nil, err
	}
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
		`SELECT id, key, name, enabled, created_at FROM api_keys WHERE id = ?`, id,
	).Scan(&k.ID, &k.Key, &k.Name, &k.Enabled, &k.CreatedAt)
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

// generateAPIKey 生成随机 API Key（sk- 前缀 + 48 位十六进制）
func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sk-" + hex.EncodeToString(b), nil
}
