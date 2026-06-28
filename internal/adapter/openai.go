package adapter

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// OpenAIAdapter OpenAI 兼容协议适配器
type OpenAIAdapter struct{}

func (a *OpenAIAdapter) GetModelsURL(baseURL, apiKey string) (string, http.Header) {
	url := strings.TrimRight(baseURL, "/") + "/v1/models"
	h := http.Header{}
	if apiKey != "" {
		h.Set("Authorization", "Bearer "+apiKey)
	}
	return url, h
}

func (a *OpenAIAdapter) ParseModelsResponse(body []byte) ([]ModelInfo, error) {
	// 先尝试解析标准 OpenAI 格式
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err == nil && len(resp.Data) > 0 {
		var models []ModelInfo
		for _, d := range resp.Data {
			m := modelDB[d.ID]
			if m == nil {
				m = &ModelInfo{ID: d.ID}
			}
			models = append(models, *m)
		}
		if len(models) > 0 {
			return models, nil
		}
	}

	// 尝试解析扩展格式（OpenRouter 等提供更多字段）
	var extResp struct {
		Data []struct {
			ID            string  `json:"id"`
			Name          string  `json:"name"`
			ContextLength int     `json:"context_length"`
			MaxOutput     int     `json:"max_output"`
			Pricing       map[string]float64 `json:"pricing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &extResp); err == nil && len(extResp.Data) > 0 {
		var models []ModelInfo
		for _, d := range extResp.Data {
			m := &ModelInfo{
				ID:              d.ID,
				Name:            d.Name,
				ContextWindow:   d.ContextLength,
				MaxOutputTokens: d.MaxOutput,
			}
			models = append(models, *m)
		}
		return models, nil
	}

	return nil, fmt.Errorf("无法解析模型列表响应")
}

// modelDB 内置模型数据库（当 API 不返回元信息时使用）
var modelDB = map[string]*ModelInfo{
	// DeepSeek
	"deepseek-chat":           {ID: "deepseek-chat", Name: "DeepSeek Chat", ContextWindow: 65536, MaxOutputTokens: 8192, SupportsTools: true},
	"deepseek-v4-flash":       {ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", ContextWindow: 1048576, MaxOutputTokens: 64000, SupportsThinking: true, SupportsTools: true},
	"deepseek-v4-pro":         {ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", ContextWindow: 1048576, MaxOutputTokens: 64000, SupportsThinking: true, SupportsTools: true},
	"deepseek-reasoner":       {ID: "deepseek-reasoner", Name: "DeepSeek Reasoner", ContextWindow: 65536, MaxOutputTokens: 8192, SupportsThinking: true},
	// OpenAI
	"gpt-4o":                  {ID: "gpt-4o", Name: "GPT-4o", ContextWindow: 128000, MaxOutputTokens: 16384, SupportsVision: true, SupportsTools: true},
	"gpt-4o-mini":             {ID: "gpt-4o-mini", Name: "GPT-4o Mini", ContextWindow: 128000, MaxOutputTokens: 16384, SupportsVision: true, SupportsTools: true},
	"gpt-4-turbo":             {ID: "gpt-4-turbo", Name: "GPT-4 Turbo", ContextWindow: 128000, MaxOutputTokens: 4096, SupportsTools: true},
	"gpt-4":                   {ID: "gpt-4", Name: "GPT-4", ContextWindow: 8192, MaxOutputTokens: 4096, SupportsTools: true},
	"gpt-3.5-turbo":           {ID: "gpt-3.5-turbo", Name: "GPT-3.5 Turbo", ContextWindow: 16385, MaxOutputTokens: 4096, SupportsTools: true},
	"o1":                      {ID: "o1", Name: "o1", ContextWindow: 200000, MaxOutputTokens: 100000, SupportsThinking: true, SupportsTools: true},
	"o1-mini":                 {ID: "o1-mini", Name: "o1 Mini", ContextWindow: 128000, MaxOutputTokens: 65536, SupportsThinking: true},
	"o3-mini":                 {ID: "o3-mini", Name: "o3 Mini", ContextWindow: 200000, MaxOutputTokens: 100000, SupportsThinking: true, SupportsTools: true},
	// Claude
	"claude-sonnet-4-20250514": {ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4 (20250514)", ContextWindow: 200000, MaxOutputTokens: 8192, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"claude-opus-4-20250514":   {ID: "claude-opus-4-20250514", Name: "Claude Opus 4 (20250514)", ContextWindow: 200000, MaxOutputTokens: 8192, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"claude-haiku-3-5-20250101": {ID: "claude-haiku-3-5-20250101", Name: "Claude Haiku 3.5 (20250101)", ContextWindow: 200000, MaxOutputTokens: 8192, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"claude-sonnet-4":          {ID: "claude-sonnet-4", Name: "Claude Sonnet 4", ContextWindow: 200000, MaxOutputTokens: 8192, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"claude-opus-4":            {ID: "claude-opus-4", Name: "Claude Opus 4", ContextWindow: 200000, MaxOutputTokens: 8192, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	// Gemini
	"gemini-2.0-flash":         {ID: "gemini-2.0-flash", Name: "Gemini 2.0 Flash", ContextWindow: 1048576, MaxOutputTokens: 8192, SupportsTools: true, SupportsVision: true},
	"gemini-2.0-flash-lite":    {ID: "gemini-2.0-flash-lite", Name: "Gemini 2.0 Flash Lite", ContextWindow: 1048576, MaxOutputTokens: 8192, SupportsTools: true, SupportsVision: true},
	"gemini-1.5-pro":           {ID: "gemini-1.5-pro", Name: "Gemini 1.5 Pro", ContextWindow: 2097152, MaxOutputTokens: 8192, SupportsTools: true, SupportsVision: true},
	"gemini-1.5-flash":         {ID: "gemini-1.5-flash", Name: "Gemini 1.5 Flash", ContextWindow: 1048576, MaxOutputTokens: 8192, SupportsTools: true, SupportsVision: true},
	// MiniMax
	"minimax-m3":               {ID: "minimax-m3", Name: "MiniMax M3", ContextWindow: 1048576, MaxOutputTokens: 16384, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"minimax-m2.7":             {ID: "minimax-m2.7", Name: "MiniMax M2.7", ContextWindow: 1048576, MaxOutputTokens: 8192, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"minimax-m2.5":             {ID: "minimax-m2.5", Name: "MiniMax M2.5", ContextWindow: 1048576, MaxOutputTokens: 8192, SupportsTools: true},
	// Kimi / Moonshot
	"kimi-k2.7-code":           {ID: "kimi-k2.7-code", Name: "Kimi K2.7 Code", ContextWindow: 262144, MaxOutputTokens: 16384, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"kimi-k2.6":                {ID: "kimi-k2.6", Name: "Kimi K2.6", ContextWindow: 262144, MaxOutputTokens: 8192, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"kimi-k2.5":                {ID: "kimi-k2.5", Name: "Kimi K2.5", ContextWindow: 262144, MaxOutputTokens: 8192, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	// GLM / 智谱
	"glm-5.2":                  {ID: "glm-5.2", Name: "GLM 5.2", ContextWindow: 131072, MaxOutputTokens: 16384, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"glm-5.1":                  {ID: "glm-5.1", Name: "GLM 5.1", ContextWindow: 131072, MaxOutputTokens: 16384, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"glm-5":                    {ID: "glm-5", Name: "GLM 5", ContextWindow: 131072, MaxOutputTokens: 16384, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"glm-4-plus":               {ID: "glm-4-plus", Name: "GLM 4 Plus", ContextWindow: 128000, MaxOutputTokens: 4096, SupportsTools: true, SupportsVision: true},
	"glm-4":                    {ID: "glm-4", Name: "GLM 4", ContextWindow: 128000, MaxOutputTokens: 4096, SupportsTools: true},
	// Qwen / 通义千问
	"qwen3.7-max":              {ID: "qwen3.7-max", Name: "Qwen 3.7 Max", ContextWindow: 131072, MaxOutputTokens: 16384, SupportsThinking: true, SupportsTools: true},
	"qwen3.7-plus":             {ID: "qwen3.7-plus", Name: "Qwen 3.7 Plus", ContextWindow: 131072, MaxOutputTokens: 16384, SupportsThinking: true, SupportsTools: true},
	"qwen3.6-plus":             {ID: "qwen3.6-plus", Name: "Qwen 3.6 Plus", ContextWindow: 131072, MaxOutputTokens: 16384, SupportsThinking: true, SupportsTools: true},
	"qwen3.5-plus":             {ID: "qwen3.5-plus", Name: "Qwen 3.5 Plus", ContextWindow: 131072, MaxOutputTokens: 16384, SupportsThinking: true, SupportsTools: true},
	"qwen-max":                 {ID: "qwen-max", Name: "Qwen Max", ContextWindow: 32768, MaxOutputTokens: 8192, SupportsTools: true},
	"qwen-plus":                {ID: "qwen-plus", Name: "Qwen Plus", ContextWindow: 131072, MaxOutputTokens: 8192, SupportsTools: true},
	"qwen-turbo":               {ID: "qwen-turbo", Name: "Qwen Turbo", ContextWindow: 131072, MaxOutputTokens: 8192, SupportsTools: true},
	"qwen2.5-72b-instruct":     {ID: "qwen2.5-72b-instruct", Name: "Qwen 2.5 72B Instruct", ContextWindow: 131072, MaxOutputTokens: 8192, SupportsTools: true},
	"qwen2.5-32b-instruct":     {ID: "qwen2.5-32b-instruct", Name: "Qwen 2.5 32B Instruct", ContextWindow: 32768, MaxOutputTokens: 8192, SupportsTools: true},
	"qwen2.5-14b-instruct":     {ID: "qwen2.5-14b-instruct", Name: "Qwen 2.5 14B Instruct", ContextWindow: 32768, MaxOutputTokens: 8192, SupportsTools: true},
	// MiMo / 小米
	"mimo-v2-pro":              {ID: "mimo-v2-pro", Name: "MiMo V2 Pro", ContextWindow: 1048576, MaxOutputTokens: 128000, SupportsThinking: true, SupportsTools: true},
	"mimo-v2-omni":             {ID: "mimo-v2-omni", Name: "MiMo V2 Omni", ContextWindow: 262144, MaxOutputTokens: 128000, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"mimo-v2.5-pro":            {ID: "mimo-v2.5-pro", Name: "MiMo V2.5 Pro", ContextWindow: 1048576, MaxOutputTokens: 128000, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"mimo-v2.5":                {ID: "mimo-v2.5", Name: "MiMo V2.5", ContextWindow: 1048576, MaxOutputTokens: 128000, SupportsThinking: true, SupportsTools: true, SupportsVision: true},
	"mimo-v2-flash":            {ID: "mimo-v2-flash", Name: "MiMo V2 Flash", ContextWindow: 262144, MaxOutputTokens: 65536, SupportsThinking: true, SupportsTools: true},
	// HY3 / 鸿源
	"hy3-preview":              {ID: "hy3-preview", Name: "HY3 Preview", ContextWindow: 131072, MaxOutputTokens: 8192, SupportsThinking: true, SupportsTools: true},
	// Doubao / 豆包
	"doubao-pro-32k":           {ID: "doubao-pro-32k", Name: "Doubao Pro 32K", ContextWindow: 32000, MaxOutputTokens: 4096, SupportsTools: true},
	"doubao-pro-128k":          {ID: "doubao-pro-128k", Name: "Doubao Pro 128K", ContextWindow: 128000, MaxOutputTokens: 4096, SupportsTools: true},
	// Yi
	"yi-lightning":             {ID: "yi-lightning", Name: "Yi Lightning", ContextWindow: 16000, MaxOutputTokens: 4096, SupportsTools: true},
	// Open source
	"llama-3.3-70b-instruct":   {ID: "llama-3.3-70b-instruct", Name: "Llama 3.3 70B Instruct", ContextWindow: 131072, MaxOutputTokens: 8192, SupportsTools: true},
	"llama-3.1-70b-instruct":   {ID: "llama-3.1-70b-instruct", Name: "Llama 3.1 70B Instruct", ContextWindow: 131072, MaxOutputTokens: 8192, SupportsTools: true},
	"llama-3.1-8b-instruct":    {ID: "llama-3.1-8b-instruct", Name: "Llama 3.1 8B Instruct", ContextWindow: 131072, MaxOutputTokens: 8192, SupportsTools: true},
	"mistral-large":             {ID: "mistral-large", Name: "Mistral Large", ContextWindow: 131072, MaxOutputTokens: 8192, SupportsTools: true},
}

func (a *OpenAIAdapter) GetChatURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
}

func (a *OpenAIAdapter) ConvertRequest(modelID string, body []byte) ([]byte, error) {
	// OpenAI 兼容协议，直接透传
	return body, nil
}

func (a *OpenAIAdapter) ConvertResponse(body []byte) ([]byte, error) {
	// OpenAI 兼容协议，直接透传
	return body, nil
}

// extractUsageFromJSON 从单一 JSON 对象中提取用量
func extractUsageFromJSON(data []byte) (*Usage, error) {
	var resp struct {
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || resp.Usage == nil {
		return nil, fmt.Errorf("无法提取用量信息")
	}
	u := &Usage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	if resp.Usage.PromptTokensDetails != nil {
		u.CacheHitTokens = resp.Usage.PromptTokensDetails.CachedTokens
	}
	return u, nil
}

// ExtractUsage 从响应中提取用量信息，支持非流式和 SSE 流式格式
func (a *OpenAIAdapter) ExtractUsage(body []byte) (*Usage, error) {
	// 先尝试作为完整 JSON 解析（非流式响应）
	if u, err := extractUsageFromJSON(body); err == nil {
		return u, nil
	}

	// 如果是 SSE 流式响应（多行 data: {...}），找最后一个含 usage 的事件
	lines := strings.Split(string(body), "\n")
	var lastUsageJSON []byte
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		// 尝试从该事件提取用量
		if u, err := extractUsageFromJSON([]byte(payload)); err == nil {
			lastUsageJSON = []byte(payload)
			_ = u // 继续找最后一个
		}
	}
	if lastUsageJSON != nil {
		return extractUsageFromJSON(lastUsageJSON)
	}

	log.Printf("[ExtractUsage] 非流式+SSE 均无法提取用量")
	return nil, fmt.Errorf("无法提取用量信息")
}
