package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	AuthIndex     string         `json:"auth_index"`
	Provider      string         `json:"provider"`
	AccountType   string         `json:"account_type"`
	Email         string         `json:"email"`
	AccountID     string         `json:"account_id"`
	PlanType      string         `json:"plan_type"`
	ProjectID     string         `json:"project_id"`
	Status        string         `json:"status"`
	StatusMessage string         `json:"status_message,omitempty"`
	Disabled      bool           `json:"disabled"`
	Unavailable   bool           `json:"unavailable"`
	Error         string         `json:"error,omitempty"`
	Raw           map[string]any `json:"-"`
}

// OAuthURLResponse 是 Management API OAuth 登录启动结果。
type OAuthURLResponse struct {
	Status string `json:"status"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Flow   string `json:"flow,omitempty"`
}

// OAuthStatusResponse 是 Management API OAuth 会话状态。
type OAuthStatusResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// StartOAuth 通过 CLIProxyAPI Management API 启动 OAuth 流程。
// provider 使用 CLIProxyAPI 的名称：anthropic、codex、antigravity、kimi、xai。
func (m *ManagementClient) StartOAuth(ctx context.Context, provider string) (*OAuthURLResponse, error) {
	var response OAuthURLResponse
	path := fmt.Sprintf("/v0/management/%s-auth-url?is_webui=true", url.PathEscape(strings.TrimSpace(provider)))
	if err := m.getJSON(ctx, path, &response); err != nil {
		return nil, err
	}
	if strings.TrimSpace(response.URL) == "" || strings.TrimSpace(response.State) == "" {
		return nil, fmt.Errorf("CPA Management API 返回了无效 OAuth 会话")
	}
	return &response, nil
}

// OAuthStatus 查询 CLIProxyAPI Management API OAuth 会话状态。
func (m *ManagementClient) OAuthStatus(ctx context.Context, state string) (*OAuthStatusResponse, error) {
	var response OAuthStatusResponse
	path := "/v0/management/get-auth-status?state=" + url.QueryEscape(strings.TrimSpace(state))
	if err := m.getJSON(ctx, path, &response); err != nil {
		return nil, err
	}
	return &response, nil
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
	return m.CallUpstreamWithBody(ctx, authIndex, method, upstreamURL, headers, nil)
}

// CallUpstreamWithBody 通过 CLIProxyAPI Management API 调用上游并携带 JSON 请求体。
func (m *ManagementClient) CallUpstreamWithBody(ctx context.Context, authIndex, method, upstreamURL string, headers map[string]string, requestBody []byte) ([]byte, int, error) {
	payload := map[string]any{
		"auth_index": authIndex,
		"method":     method,
		"url":        upstreamURL,
		"header":     headers,
	}
	if len(requestBody) > 0 {
		payload["data"] = string(requestBody)
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
	file.ID = readString(raw, "id")
	file.Name = readString(raw, "name")
	file.AuthIndex = readString(raw, "auth_index", "authIndex", "AuthIndex", "auth-index")
	file.Provider = readString(raw, "provider", "type")
	file.AccountType = readString(raw, "account_type", "accountType")
	file.Email = readString(raw, "email")
	file.AccountID = readString(raw, "chatgpt_account_id", "account_id")
	file.PlanType = readString(raw, "plan_type", "planType")
	file.ProjectID = readString(raw, "project_id", "projectId")
	file.Status = readString(raw, "status")
	file.StatusMessage = readString(raw, "status_message", "statusMessage")
	file.Disabled = readBool(raw, "disabled")
	file.Unavailable = readBool(raw, "unavailable")
	if claims, ok := raw["id_token"].(map[string]any); ok {
		if file.AccountID == "" {
			file.AccountID = readString(claims, "chatgpt_account_id", "account_id")
		}
		if file.PlanType == "" {
			file.PlanType = readString(claims, "plan_type", "planType")
		}
	}
	if file.AccountID == "" && strings.EqualFold(file.AccountType, "oauth") {
		file.AccountID = readString(raw, "account")
	}
	if file.ProjectID == "" {
		for _, key := range []string{"metadata", "attributes"} {
			if nested, ok := raw[key].(map[string]any); ok {
				file.ProjectID = readString(nested, "project_id", "projectId", "gemini_virtual_project")
				if file.ProjectID != "" {
					break
				}
			}
		}
	}
	return file
}

func readString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			switch v := value.(type) {
			case string:
				if text := strings.TrimSpace(v); text != "" {
					return text
				}
			case json.Number:
				return v.String()
			case float64:
				return fmt.Sprintf("%v", v)
			}
		}
	}
	return ""
}

func readBool(m map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			if result, ok := value.(bool); ok {
				return result
			}
		}
	}
	return false
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
