package cpa

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// QuotaProvider 为后续 Claude/Grok/Kimi 等额度 provider 预留统一接口。
type QuotaProvider interface {
	ID() string
	Match(AuthFile) bool
	Query(context.Context, *ManagementClient, AuthFile) (*QuotaSnapshot, error)
}

// QuotaWindow 统一表示一个额度窗口。
type QuotaWindow struct {
	ID                string     `json:"id"`
	Label             string     `json:"label"`
	UsedPercent       *float64   `json:"used_percent,omitempty"`
	RemainingPercent  *float64   `json:"remaining_percent,omitempty"`
	ResetAt           *time.Time `json:"reset_at,omitempty"`
	ResetAfterSeconds *int64     `json:"reset_after_seconds,omitempty"`
}

type QuotaSnapshot struct {
	Provider     string       `json:"provider"`
	AuthIndex    string       `json:"auth_index"`
	AccountID    string       `json:"account_id,omitempty"`
	Email        string       `json:"email,omitempty"`
	PlanType     string       `json:"plan_type,omitempty"`
	Status       string       `json:"status"`
	FiveHour     *QuotaWindow `json:"five_hour,omitempty"`
	Weekly       *QuotaWindow `json:"weekly,omitempty"`
	ResetCredits int64        `json:"reset_credits,omitempty"`
	QueriedAt    time.Time    `json:"queried_at"`
	Error        string       `json:"error,omitempty"`
}

type CodexQuotaProvider struct{}

func (CodexQuotaProvider) ID() string { return "codex" }

func (CodexQuotaProvider) Match(auth AuthFile) bool {
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") || auth.AuthIndex == "" {
		return false
	}
	// auth-files 的 account_type 在不同 CLIProxyAPI 版本中可能缺失；
	// 仅明确标记为 API Key 时排除，未标记的 codex auth 默认视为 OAuth 订阅。
	return !strings.EqualFold(auth.AccountType, "api_key") && !strings.EqualFold(auth.AccountType, "apikey")
}

func (CodexQuotaProvider) Query(ctx context.Context, client *ManagementClient, auth AuthFile) (*QuotaSnapshot, error) {
	body, status, err := client.CallUpstream(ctx, auth.AuthIndex, "GET", "https://chatgpt.com/backend-api/wham/usage", map[string]string{
		"Authorization":      "Bearer $TOKEN$",
		"Accept":             "application/json",
		"Content-Type":       "application/json",
		"User-Agent":         "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal",
		"Chatgpt-Account-Id": auth.AccountID,
	})
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Codex usage API 返回 HTTP %d", status)
	}
	return parseCodexUsage(body, auth)
}

func parseCodexUsage(body []byte, auth AuthFile) (*QuotaSnapshot, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析 Codex usage 响应失败: %w", err)
	}
	snapshot := &QuotaSnapshot{
		Provider: "codex", AuthIndex: auth.AuthIndex, AccountID: auth.AccountID,
		Email: auth.Email, PlanType: auth.PlanType, Status: "available", QueriedAt: time.Now().UTC(),
	}
	if value, ok := raw["plan_type"].(string); ok && value != "" {
		snapshot.PlanType = value
	}
	rate, _ := raw["rate_limit"].(map[string]any)
	if rate == nil {
		return nil, fmt.Errorf("Codex usage 响应缺少 rate_limit")
	}
	// Codex 当前可能只返回 primary_window，但它实际代表 7 天窗口；
	// 优先使用 limit_window_seconds 判断，不能仅按 primary/secondary 字段名归类。
	for key, value := range rate {
		windowRaw, ok := value.(map[string]any)
		if !ok || (key != "primary_window" && key != "secondary_window" && key != "primaryWindow" && key != "secondaryWindow") {
			continue
		}
		window := parseCodexWindow(key, windowRaw)
		if window == nil {
			continue
		}
		switch window.ID {
		case "code-5h":
			snapshot.FiveHour = window
		case "code-7d":
			snapshot.Weekly = window
		}
	}
	if credits, ok := raw["rate_limit_reset_credits"].(map[string]any); ok {
		if value, ok := readNumber(credits, "available_count"); ok {
			snapshot.ResetCredits = int64(value)
		}
	}
	if allowed, ok := rate["allowed"].(bool); ok && !allowed {
		snapshot.Status = "limited"
	}
	if snapshot.FiveHour == nil && snapshot.Weekly == nil {
		return nil, fmt.Errorf("Codex usage 响应缺少额度窗口")
	}
	return snapshot, nil
}

func parseCodexWindow(key string, raw map[string]any) *QuotaWindow {
	if seconds, ok := readNumber(raw, "limit_window_seconds", "limitWindowSeconds"); ok {
		switch int64(seconds) {
		case 5 * 60 * 60:
			return parseQuotaWindow("code-5h", "5h", raw)
		case 7 * 24 * 60 * 60:
			return parseQuotaWindow("code-7d", "7d", raw)
		}
	}
	// 旧版响应没有窗口时长字段时，保留字段名回退规则。
	switch key {
	case "primary_window", "primaryWindow":
		return parseQuotaWindow("code-5h", "5h", raw)
	case "secondary_window", "secondaryWindow":
		return parseQuotaWindow("code-7d", "7d", raw)
	default:
		return nil
	}
}

func parseQuotaWindow(id, label string, raw map[string]any) *QuotaWindow {
	window := &QuotaWindow{ID: id, Label: label}
	if used, ok := readNumber(raw, "used_percent", "usedPercent"); ok {
		window.UsedPercent = &used
		remaining := 100 - used
		window.RemainingPercent = &remaining
	}
	if timestamp, ok := readNumber(raw, "reset_at", "resetAt"); ok {
		value := time.Unix(int64(timestamp), 0).UTC()
		window.ResetAt = &value
	}
	if seconds, ok := readNumber(raw, "reset_after_seconds", "resetAfterSeconds"); ok {
		value := int64(seconds)
		window.ResetAfterSeconds = &value
	}
	return window
}
