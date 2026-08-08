package adapter

import "net/http"

// Usage 从上游响应中提取的用量信息
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheHitTokens   int `json:"cache_hit_tokens"`
}

// Adapter 定义协议适配器接口
type Adapter interface {
	// 获取上游模型列表的 URL 和需要的处理
	GetModelsURL(baseURL, apiKey string) (url string, headers http.Header)

	// 解析上游模型列表响应
	ParseModelsResponse(body []byte) ([]ModelInfo, error)

	// 获取聊天补全的 URL
	GetChatURL(baseURL string) string

	// 是否需要转换请求体
	ConvertRequest(modelID string, body []byte) ([]byte, error)

	// 是否需要转换响应体
	ConvertResponse(body []byte) ([]byte, error)

	// 从响应中提取用量信息
	ExtractUsage(body []byte) (*Usage, error)

	// NewStreamConverter 返回将上游 SSE 流转换为 OpenAI 规范格式 SSE 的转换器
	// 上游协议本身就是 OpenAI 兼容格式（规范格式）时返回 nil，表示无需转换、原样透传
	NewStreamConverter() StreamConverter
}

// ModelInfo 上游模型信息
type ModelInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ContextWindow   int    `json:"context_window"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	SupportsVision  bool   `json:"supports_vision"`
	SupportsThinking bool  `json:"supports_thinking"`
	SupportsTools   bool   `json:"supports_tools"`
}

// NewAdapter 根据渠道类型创建适配器
func NewAdapter(channelType string) Adapter {
	switch channelType {
	case "anthropic":
		return &AnthropicAdapter{}
	case "gemini":
		return &GeminiAdapter{}
	case "responses":
		return &ResponsesAdapter{}
	case "openai", "openrouter":
		// openrouter 为 OpenAI 兼容协议（历史遗留值，已并入 openai）
		return &OpenAIAdapter{}
	default:
		return &OpenAIAdapter{}
	}
}

// GetModelDBInfo 从内置模型数据库中查找模型信息
// 供上游同步器在合并优先级时使用
func GetModelDBInfo(modelID string) *ModelInfo {
	if m, ok := modelDB[modelID]; ok {
		return m
	}
	return nil
}
