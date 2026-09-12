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
	Provider          string                  `json:"provider"`
	AuthIndex         string                  `json:"auth_index"`
	CredentialName    string                  `json:"credential_name,omitempty"`
	AccountID         string                  `json:"account_id,omitempty"`
	Email             string                  `json:"email,omitempty"`
	PlanType          string                  `json:"plan_type,omitempty"`
	Status            string                  `json:"status"`
	FiveHour          *QuotaWindow            `json:"five_hour,omitempty"`
	Weekly            *QuotaWindow            `json:"weekly,omitempty"`
	AntigravityGroups []AntigravityQuotaGroup `json:"antigravity_groups,omitempty"`
	ResetCredits      int64                   `json:"reset_credits,omitempty"`
	AICredits         *int64                  `json:"ai_credits,omitempty"`
	AICreditsMinimum  *int64                  `json:"ai_credits_minimum,omitempty"`
	QueriedAt         time.Time               `json:"queried_at"`
	Error             string                  `json:"error,omitempty"`
}

type AntigravityQuotaGroup struct {
	ID          string                   `json:"id"`
	Label       string                   `json:"label"`
	Description string                   `json:"description,omitempty"`
	Buckets     []AntigravityQuotaBucket `json:"buckets"`
}

type AntigravityQuotaBucket struct {
	ID               string     `json:"id"`
	Label            string     `json:"label"`
	Window           string     `json:"window,omitempty"`
	RemainingPercent float64    `json:"remaining_percent"`
	ResetAt          *time.Time `json:"reset_at,omitempty"`
	Description      string     `json:"description,omitempty"`
}

type CodexQuotaProvider struct{}

type AntigravityQuotaProvider struct{}

func (CodexQuotaProvider) ID() string       { return "codex" }
func (AntigravityQuotaProvider) ID() string { return "antigravity" }

var antigravityQuotaURLs = []string{
	"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary",
	"https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
}

func (AntigravityQuotaProvider) Match(auth AuthFile) bool {
	return authUsable(auth) && strings.EqualFold(strings.TrimSpace(auth.Provider), "antigravity")
}

func authUsable(auth AuthFile) bool {
	if strings.TrimSpace(auth.AuthIndex) == "" || auth.Disabled || auth.Unavailable {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(auth.Status)) {
	case "disabled", "error", "revoked", "unavailable":
		return false
	}
	return true
}

func (AntigravityQuotaProvider) Query(ctx context.Context, client *ManagementClient, auth AuthFile) (*QuotaSnapshot, error) {
	if strings.TrimSpace(auth.ProjectID) == "" {
		return nil, fmt.Errorf("Antigravity 认证信息缺少 project_id")
	}
	requestBody, err := json.Marshal(map[string]string{"project": auth.ProjectID})
	if err != nil {
		return nil, fmt.Errorf("序列化 Antigravity 额度请求失败: %w", err)
	}
	headers := map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Content-Type":  "application/json",
		"User-Agent":    "antigravity/cli/1.0.13 (aidev_client; os_type=darwin; arch=arm64)",
	}
	var lastError error
	for _, endpoint := range antigravityQuotaURLs {
		body, status, callErr := client.CallUpstreamWithBody(ctx, auth.AuthIndex, "POST", endpoint, headers, requestBody)
		if callErr != nil {
			lastError = callErr
			continue
		}
		if status < 200 || status >= 300 {
			lastError = fmt.Errorf("Antigravity 额度 API 返回 HTTP %d: %s", status, truncateQuotaBody(body))
			continue
		}
		snapshot, parseErr := parseAntigravityQuotaSummary(body, auth)
		if parseErr != nil {
			lastError = parseErr
			continue
		}
		return snapshot, nil
	}
	if lastError == nil {
		lastError = fmt.Errorf("Antigravity 额度 API 无可用端点")
	}
	return nil, lastError
}

func parseAntigravityQuotaSummary(body []byte, auth AuthFile) (*QuotaSnapshot, error) {
	var raw struct {
		Groups []struct {
			DisplayName string `json:"displayName"`
			Description string `json:"description"`
			Buckets     []struct {
				BucketID          string      `json:"bucketId"`
				DisplayName       string      `json:"displayName"`
				Window            string      `json:"window"`
				ResetTime         string      `json:"resetTime"`
				RemainingFraction interface{} `json:"remainingFraction"`
				Description       string      `json:"description"`
			} `json:"buckets"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析 Antigravity 额度响应失败: %w", err)
	}
	snapshot := &QuotaSnapshot{Provider: "antigravity", AuthIndex: auth.AuthIndex, AccountID: auth.AccountID, Email: auth.Email, PlanType: auth.PlanType, Status: "available", QueriedAt: time.Now().UTC()}
	for groupIndex, rawGroup := range raw.Groups {
		group := AntigravityQuotaGroup{
			ID:    stableQuotaID(rawGroup.DisplayName, fmt.Sprintf("group-%d", groupIndex+1)),
			Label: strings.TrimSpace(rawGroup.DisplayName), Description: strings.TrimSpace(rawGroup.Description),
			Buckets: []AntigravityQuotaBucket{},
		}
		for bucketIndex, rawBucket := range rawGroup.Buckets {
			fraction, ok := quotaFloat64(rawBucket.RemainingFraction)
			if !ok {
				continue
			}
			fraction = max(0, min(1, fraction))
			bucket := AntigravityQuotaBucket{
				ID: strings.TrimSpace(rawBucket.BucketID), Label: strings.TrimSpace(rawBucket.DisplayName),
				Window: strings.TrimSpace(rawBucket.Window), RemainingPercent: fraction * 100,
				Description: strings.TrimSpace(rawBucket.Description),
			}
			if bucket.ID == "" {
				bucket.ID = fmt.Sprintf("%s-bucket-%d", group.ID, bucketIndex+1)
			}
			if resetAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(rawBucket.ResetTime)); err == nil {
				resetAt = resetAt.UTC()
				bucket.ResetAt = &resetAt
			}
			group.Buckets = append(group.Buckets, bucket)
		}
		if len(group.Buckets) > 0 {
			snapshot.AntigravityGroups = append(snapshot.AntigravityGroups, group)
		}
	}
	if len(snapshot.AntigravityGroups) == 0 {
		return nil, fmt.Errorf("Antigravity 额度响应缺少有效额度分组")
	}
	return snapshot, nil
}

func stableQuotaID(label, fallback string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(label)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			lastDash = false
		} else if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return fallback
	}
	return result
}

func quotaFloat64(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		result, err := v.Float64()
		return result, err == nil
	case string:
		if strings.TrimSpace(v) == "" {
			return 0, false
		}
		var result float64
		_, err := fmt.Sscan(strings.TrimSpace(v), &result)
		return result, err == nil
	default:
		return 0, false
	}
}

func truncateQuotaBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 512 {
		return text[:512] + "..."
	}
	if text == "" {
		return "无响应正文"
	}
	return text
}

func (CodexQuotaProvider) Match(auth AuthFile) bool {
	if !authUsable(auth) || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
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
		return nil, fmt.Errorf("Codex usage API 返回 HTTP %d: %s", status, truncateQuotaBody(body))
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
