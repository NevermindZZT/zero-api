package adapter

import "strings"

// CopyResponsesSessionHeaders 复制 Responses Agent 会话关联头。
// CLIProxyAPI 使用这些头维持 Codex reasoning replay 和 tool call 关联；
// 仅允许明确列出的非敏感头，认证始终由渠道配置单独处理。
func CopyResponsesSessionHeaders(src map[string][]string, dst func(string, string)) {
	for name, values := range src {
		if !IsResponsesSessionHeader(name) {
			continue
		}
		for _, value := range values {
			dst(name, value)
		}
	}
}

// IsResponsesSessionHeader 判断是否为 Responses Agent 会话关联头。
func IsResponsesSessionHeader(name string) bool {
	switch strings.ToLower(name) {
	case "openai-conversation-id", "openai-organization", "openai-project",
		"x-codex-session-id", "x-openai-client-user-agent",
		"x-stainless-lang", "x-stainless-package-version":
		return true
	default:
		return false
	}
}
