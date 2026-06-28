package adapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// GeminiAdapter Google Gemini API 适配器
type GeminiAdapter struct{}

func (a *GeminiAdapter) GetModelsURL(baseURL, apiKey string) (string, http.Header) {
	url := strings.TrimRight(baseURL, "/") + "/v1beta/models"
	if apiKey != "" {
		url = fmt.Sprintf("%s?key=%s", url, apiKey)
	}
	return url, nil
}

func (a *GeminiAdapter) ParseModelsResponse(body []byte) ([]ModelInfo, error) {
	var resp struct {
		Models []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	var models []ModelInfo
	for _, m := range resp.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		models = append(models, ModelInfo{ID: id})
	}
	return models, nil
}

func (a *GeminiAdapter) GetChatURL(baseURL string) string {
	// Gemini 使用不同的端点格式: /v1beta/models/{model}:generateContent
	// 这里返回基础 URL，实际请求时需要附加模型名
	return strings.TrimRight(baseURL, "/") + "/v1beta/models"
}

func (a *GeminiAdapter) ConvertRequest(modelID string, body []byte) ([]byte, error) {
	// 将 OpenAI 格式转为 Gemini 格式
	var openAIReq struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		return body, nil
	}

	geminiReq := GeminiRequest{
		Contents: []GeminiContent{},
	}

	for _, msg := range openAIReq.Messages {
		role := "user"
		if msg.Role == "assistant" {
			role = "model"
		} else if msg.Role == "system" || msg.Role == "developer" {
			// Gemini 不支持 system prompt 在请求体中
			role = "user"
		}

		geminiReq.Contents = append(geminiReq.Contents, GeminiContent{
			Role: role,
			Parts: []GeminiPart{
				{Text: msg.Content},
			},
		})
	}

	return json.Marshal(geminiReq)
}

func (a *GeminiAdapter) ConvertResponse(body []byte) ([]byte, error) {
	var geminiResp GeminiResponse
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return body, nil
	}

	content := ""
	if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
		content = geminiResp.Candidates[0].Content.Parts[0].Text
	}

	openAIResp := OpenAIResponse{
		ID:     "chatcmpl-gemini",
		Object: "chat.completion",
		Choices: []OpenAIChoice{
			{
				Index: 0,
				Message: OpenAIMessage{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: geminiResp.Candidates[0].FinishReason,
			},
		},
		Usage: Usage{
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
		},
	}

	return json.Marshal(openAIResp)
}

func (a *GeminiAdapter) ExtractUsage(body []byte) (*Usage, error) {
	// 尝试从 OpenAI 格式提取（已经 ConvertResponse 转换过了）
	var resp OpenAIResponse
	if err := json.Unmarshal(body, &resp); err == nil && resp.Usage.TotalTokens > 0 {
		return &resp.Usage, nil
	}

	// 尝试从 Gemini 原始格式提取
	var geminiResp GeminiResponse
	if err := json.Unmarshal(body, &geminiResp); err == nil {
		return &Usage{
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
		}, nil
	}

	return nil, fmt.Errorf("无法提取用量信息")
}

type GeminiRequest struct {
	Contents []GeminiContent `json:"contents"`
}

type GeminiContent struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text string `json:"text"`
}

type GeminiResponse struct {
	Candidates []struct {
		Content      GeminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}
