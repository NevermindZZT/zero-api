package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AnthropicAdapter Anthropic Messages API 适配器
type AnthropicAdapter struct{}

func (a *AnthropicAdapter) GetModelsURL(baseURL, apiKey string) (string, http.Header) {
	// Anthropic 没有公开的模型列表 API，返回空
	url := strings.TrimRight(baseURL, "/") + "/v1/models"
	h := http.Header{}
	if apiKey != "" {
		h.Set("x-api-key", apiKey)
		h.Set("anthropic-version", "2023-06-01")
	}
	return url, h
}

func (a *AnthropicAdapter) ParseModelsResponse(body []byte) ([]ModelInfo, error) {
	// Anthropic 模型列表相对固定
	return []ModelInfo{
		{ID: "claude-sonnet-4-20250514", ContextWindow: 200000, SupportsThinking: true, SupportsTools: true},
		{ID: "claude-opus-4-20250514", ContextWindow: 200000, SupportsThinking: true, SupportsTools: true},
		{ID: "claude-haiku-3-5-20250101", ContextWindow: 200000, SupportsThinking: true, SupportsTools: true},
	}, nil
}

func (a *AnthropicAdapter) GetChatURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/messages"
}

func (a *AnthropicAdapter) ConvertRequest(modelID string, body []byte) ([]byte, error) {
	// 将 OpenAI 格式转为 Anthropic 格式
	var openAIReq struct {
		Model       string          `json:"model"`
		Messages    []OpenAIMessage `json:"messages"`
		MaxTokens   int             `json:"max_tokens"`
		Temperature float64         `json:"temperature"`
		Stream      bool            `json:"stream"`
	}
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		return body, nil // 透传
	}

	// 转换为 Anthropic 格式
	anthropicReq := AnthropicRequest{
		Model:         modelID,
		MaxTokens:     4096,
		System:        "",
		Messages:      []AnthropicMessage{},
		Stream:        openAIReq.Stream,
	}

	if openAIReq.MaxTokens > 0 {
		anthropicReq.MaxTokens = openAIReq.MaxTokens
	}

	// 转换 messages
	for _, msg := range openAIReq.Messages {
		role := msg.Role
		if role == "system" {
			anthropicReq.System = msg.Content
			continue
		}
		// Anthropic 使用 "assistant" 和 "user"
		if role == "developer" {
			role = "assistant"
		}
		anthropicReq.Messages = append(anthropicReq.Messages, AnthropicMessage{
			Role:    role,
			Content: msg.Content,
		})
	}

	return json.Marshal(anthropicReq)
}

func (a *AnthropicAdapter) ConvertResponse(body []byte) ([]byte, error) {
	// 将 Anthropic 格式转为 OpenAI 格式
	var anthropicResp AnthropicResponse
	if err := json.Unmarshal(body, &anthropicResp); err != nil {
		return body, nil
	}

	// 构造 OpenAI 格式响应
	openAIResp := OpenAIResponse{
		ID:      fmt.Sprintf("chatcmpl-%s", anthropicResp.ID),
		Object:  "chat.completion",
		Created: 0,
		Model:   anthropicResp.Model,
		Choices: []OpenAIChoice{
			{
				Index: 0,
				Message: OpenAIMessage{
					Role:    "assistant",
					Content: "",
				},
				FinishReason: anthropicResp.StopReason,
			},
		},
		Usage: Usage{
			PromptTokens:     anthropicResp.Usage.InputTokens,
			CompletionTokens: anthropicResp.Usage.OutputTokens,
			TotalTokens:      anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens,
		},
	}

	// 提取 content
	for _, block := range anthropicResp.Content {
		if block.Type == "text" {
			openAIResp.Choices[0].Message.Content = block.Text
			break
		}
	}

	return json.Marshal(openAIResp)
}

func (a *AnthropicAdapter) ExtractUsage(body []byte) (*Usage, error) {
	// 尝试从 OpenAI 格式提取（已经 ConvertResponse 转换过了）
	var resp OpenAIResponse
	if err := json.Unmarshal(body, &resp); err == nil && resp.Usage.TotalTokens > 0 {
		return &resp.Usage, nil
	}

	// 尝试从 Anthropic 原始格式提取
	var anthropicResp AnthropicResponse
	if err := json.Unmarshal(body, &anthropicResp); err == nil {
		return &Usage{
			PromptTokens:     anthropicResp.Usage.InputTokens,
			CompletionTokens: anthropicResp.Usage.OutputTokens,
			TotalTokens:      anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens,
		}, nil
	}

	return nil, fmt.Errorf("无法提取用量信息")
}

// 请求/响应结构体
type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type AnthropicRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system,omitempty"`
	Messages  []AnthropicMessage  `json:"messages"`
	Stream    bool                `json:"stream,omitempty"`
}

type AnthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type AnthropicResponse struct {
	ID         string              `json:"id"`
	Type       string              `json:"type"`
	Model      string              `json:"model"`
	Content    []AnthropicContent  `json:"content"`
	StopReason string              `json:"stop_reason"`
	Usage      AnthropicUsage      `json:"usage"`
}

type AnthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type OpenAIResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []OpenAIChoice `json:"choices"`
	Usage   Usage          `json:"usage"`
}

type OpenAIChoice struct {
	Index        int          `json:"index"`
	Message      OpenAIMessage `json:"message"`
	FinishReason string       `json:"finish_reason"`
}
