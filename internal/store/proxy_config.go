package store

import (
	"encoding/json"
	"time"
)

// ModelMapping 模型映射配置（与 ModelProxy 兼容）
type ModelMapping struct {
	TargetModel     string `json:"target_model"`
	Name            string `json:"name"`
	ContextWindow   int    `json:"context_window"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Thinking        bool   `json:"thinking"`
	ReasoningEffort string `json:"reasoning_effort"`
	Vision          bool   `json:"vision"`
}

// ProxyConfig 代理配置
type ProxyConfigData struct {
	ID                    int64                  `json:"id"`
	InterceptDomains      []string               `json:"intercept_domains"`
	SmartInterceptDomains []string               `json:"smart_intercept_domains"`
	DefaultChannelID      *int64                 `json:"default_channel_id,omitempty"`
	ModelMappings         map[string]ModelMapping `json:"model_mappings"`
	MitmAll               bool                   `json:"mitm_all"`
	ProxyUsername         string                 `json:"proxy_username"`
	ProxyPassword         string                 `json:"proxy_password"`
	ForwardProxyURL       string                 `json:"forward_proxy_url"`
	ForwardProxyUser      string                 `json:"forward_proxy_user"`
	ForwardProxyPass      string                 `json:"forward_proxy_pass"`
	ProbeAPIKey           string                 `json:"probe_api_key"`
	RequestTimeoutSeconds int                    `json:"request_timeout_seconds"`
	FailoverEnabled       bool                   `json:"failover_enabled"` // 全局熔断开关
	CreatedAt             time.Time              `json:"created_at"`
	UpdatedAt             time.Time              `json:"updated_at"`
}


type ProxyConfigRepo struct {
	db *DB
}

func NewProxyConfigRepo(db *DB) *ProxyConfigRepo {
	return &ProxyConfigRepo{db: db}
}

func (r *ProxyConfigRepo) Get() (*ProxyConfigData, error) {
	c := &ProxyConfigData{}
	var interceptJSON, smartJSON, mappingsJSON string
	err := r.db.QueryRow(
		`SELECT id, intercept_domains, smart_intercept_domains, default_channel_id, model_mappings, mitm_all, proxy_username, proxy_password, forward_proxy_url, forward_proxy_user, forward_proxy_pass, probe_api_key, request_timeout_seconds, failover_enabled, created_at, updated_at FROM proxy_config LIMIT 1`,
	).Scan(&c.ID, &interceptJSON, &smartJSON, &c.DefaultChannelID, &mappingsJSON, &c.MitmAll, &c.ProxyUsername, &c.ProxyPassword, &c.ForwardProxyURL, &c.ForwardProxyUser, &c.ForwardProxyPass, &c.ProbeAPIKey, &c.RequestTimeoutSeconds, &c.FailoverEnabled, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(interceptJSON), &c.InterceptDomains)
	json.Unmarshal([]byte(smartJSON), &c.SmartInterceptDomains)
	json.Unmarshal([]byte(mappingsJSON), &c.ModelMappings)
	if c.InterceptDomains == nil {
		c.InterceptDomains = []string{}
	}
	if c.SmartInterceptDomains == nil {
		c.SmartInterceptDomains = []string{}
	}
	if c.ModelMappings == nil {
		c.ModelMappings = map[string]ModelMapping{}
	}
	return c, nil
}

func (r *ProxyConfigRepo) Update(c *ProxyConfigData) error {
	interceptJSON, _ := json.Marshal(c.InterceptDomains)
	smartJSON, _ := json.Marshal(c.SmartInterceptDomains)
	mappingsJSON, _ := json.Marshal(c.ModelMappings)

	_, err := r.db.Exec(
		`UPDATE proxy_config SET intercept_domains=?, smart_intercept_domains=?, default_channel_id=?, model_mappings=?, mitm_all=?, proxy_username=?, proxy_password=?, forward_proxy_url=?, forward_proxy_user=?, forward_proxy_pass=?, probe_api_key=?, request_timeout_seconds=?, failover_enabled=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		string(interceptJSON), string(smartJSON), c.DefaultChannelID, string(mappingsJSON), c.MitmAll, c.ProxyUsername, c.ProxyPassword, c.ForwardProxyURL, c.ForwardProxyUser, c.ForwardProxyPass, c.ProbeAPIKey, c.RequestTimeoutSeconds, c.FailoverEnabled, c.ID,
	)
	return err
}
