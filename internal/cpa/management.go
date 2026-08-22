package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ManagementClient 调用 CLIProxyAPI 本机 Management API。
type ManagementClient struct {
	mu      sync.RWMutex
	BaseURL string
	Key     string
	HTTP    *http.Client
}

func NewManagementClient(host string, port int, key string) *ManagementClient {
	if host == "" {
		host = "127.0.0.1"
	}
	return &ManagementClient{
		BaseURL: fmt.Sprintf("http://%s:%d", host, port),
		Key:     key,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// UpdateEndpoint 在 CPA 配置变化后同步 Management API 地址。
func (m *ManagementClient) UpdateEndpoint(host string, port int) {
	if m == nil || port <= 0 {
		return
	}
	if host == "" {
		host = "127.0.0.1"
	}
	m.mu.Lock()
	m.BaseURL = fmt.Sprintf("http://%s:%d", host, port)
	m.mu.Unlock()
}

type AuthFile struct {
	ID          string         `json:"id"`
	AuthIndex   string         `json:"auth_index"`
	Provider    string         `json:"provider"`
	AccountType string         `json:"account_type"`
	Email       string         `json:"email"`
	AccountID   string         `json:"account_id"`
	PlanType    string         `json:"plan_type"`
	Status      string         `json:"status"`
	Error       string         `json:"error,omitempty"`
	Raw         map[string]any `json:"-"`
}

func (m *ManagementClient) GetAuthFiles(ctx context.Context) ([]AuthFile, error) {
	var envelope struct {
		Files []map[string]any `json:"files"`
	}
	if err := m.getJSON(ctx, "/v0/management/auth-files", &envelope); err != nil {
		return nil, err
	}
	files := make([]AuthFile, 0, len(envelope.Files))
	for _, raw := range envelope.Files {
		files = append(files, authFileFromMap(raw))
	}
	return files, nil
}

func (m *ManagementClient) CallUpstream(ctx context.Context, authIndex, method, upstreamURL string, headers map[string]string) ([]byte, int, error) {
	payload := map[string]any{
		"auth_index": authIndex,
		"method":     method,
		"url":        upstreamURL,
		"header":     headers,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	var response struct {
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
		Error      string `json:"error"`
	}
	if err := m.postJSON(ctx, "/v0/management/api-call", body, &response); err != nil {
		return nil, 0, err
	}
	if response.Error != "" {
		return nil, response.StatusCode, fmt.Errorf("CPA api-call: %s", response.Error)
	}
	return []byte(response.Body), response.StatusCode, nil
}

func (m *ManagementClient) getJSON(ctx context.Context, path string, out any) error {
	return m.doJSON(ctx, http.MethodGet, path, nil, out)
}

func (m *ManagementClient) postJSON(ctx context.Context, path string, body []byte, out any) error {
	return m.doJSON(ctx, http.MethodPost, path, body, out)
}

func (m *ManagementClient) doJSON(ctx context.Context, method, path string, body []byte, out any) error {
	if m == nil || m.HTTP == nil {
		return fmt.Errorf("CPA Management API client 未初始化")
	}
	m.mu.RLock()
	baseURL := m.BaseURL
	key := m.Key
	httpClient := m.HTTP
	m.mu.RUnlock()
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求 CPA Management API 失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("CPA Management API 返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("解析 CPA Management API 响应失败: %w", err)
	}
	return nil
}

func authFileFromMap(raw map[string]any) AuthFile {
	file := AuthFile{Raw: raw}
	file.ID, _ = raw["id"].(string)
	file.AuthIndex, _ = raw["auth_index"].(string)
	file.Provider, _ = raw["provider"].(string)
	file.AccountType, _ = raw["account_type"].(string)
	file.Email, _ = raw["email"].(string)
	file.AccountID, _ = raw["chatgpt_account_id"].(string)
	file.PlanType, _ = raw["plan_type"].(string)
	file.Status, _ = raw["status"].(string)
	if claims, ok := raw["id_token"].(map[string]any); ok {
		if file.AccountID == "" {
			file.AccountID, _ = claims["chatgpt_account_id"].(string)
		}
		if file.PlanType == "" {
			file.PlanType, _ = claims["plan_type"].(string)
		}
	}
	return file
}

func readNumber(m map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			switch v := value.(type) {
			case float64:
				return v, true
			case int:
				return float64(v), true
			case json.Number:
				f, err := v.Float64()
				if err == nil {
					return f, true
				}
			}
		}
	}
	return 0, false
}
