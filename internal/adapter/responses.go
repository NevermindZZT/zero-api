package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ResponsesAdapter OpenAI Responses API 上游适配器
// 上游渠道类型为 "responses"，端点 POST /v1/responses
type ResponsesAdapter struct{}

// errNoUsage 无法提取用量信息的错误
var errNoUsage = fmt.Errorf("无法提取用量信息")

func (a *ResponsesAdapter) GetModelsURL(baseURL, apiKey string) (string, http.Header) {
	url := strings.TrimRight(baseURL, "/") + "/v1/models"
	h := http.Header{}
	if apiKey != "" {
		h.Set("Authorization", "Bearer "+apiKey)
	}
	return url, h
}

func (a *ResponsesAdapter) ParseModelsResponse(body []byte) ([]ModelInfo, error) {
	// 复用 OpenAI 的解析逻辑（Responses API 供应商同样提供 /v1/models）
	oa := &OpenAIAdapter{}
	return oa.ParseModelsResponse(body)
}

func (a *ResponsesAdapter) GetChatURL(baseURL string) string {
	return strings.TrimRight(baseURL, "/") + "/v1/responses"
}

// ConvertRequest 将 OpenAI 规范格式（Chat Completions）请求转换为 Responses API 格式
func (a *ResponsesAdapter) ConvertRequest(modelID string, body []byte) ([]byte, error) {
	var req struct {
		Model    string    `json:"model"`
		Messages []struct {
			Role         string          `json:"role"`
			Content      string          `json:"content"`
			ToolCalls    []openAIToolCall `json:"tool_calls"`
			ToolCallID   string          `json:"tool_call_id"`
		} `json:"messages"`
		MaxTokens   int             `json:"max_tokens"`
		Temperature *float64        `json:"temperature"`
		TopP        *float64        `json:"top_p"`
		Stop        json.RawMessage `json:"stop"`
		Stream      bool            `json:"stream"`
		Tools       []openAITool    `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return body, nil // 透传
	}

	// 构建 Responses 请求
	respReq := map[string]interface{}{
		"model":  modelID,
		"stream": req.Stream,
	}
	if req.MaxTokens > 0 {
		respReq["max_output_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		respReq["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		respReq["top_p"] = *req.TopP
	}

	// stop（字符串或数组）
	if len(req.Stop) > 0 && string(req.Stop) != "null" {
		var stopStr string
		if err := json.Unmarshal(req.Stop, &stopStr); err == nil {
			respReq["stop"] = []string{stopStr}
		} else {
			var stopArr []string
			if err := json.Unmarshal(req.Stop, &stopArr); err == nil {
				respReq["stop"] = stopArr
			}
		}
	}

	// tools → Responses function tools
	if len(req.Tools) > 0 {
		var tools []map[string]interface{}
		for _, t := range req.Tools {
			tools = append(tools, map[string]interface{}{
				"type":        "function",
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  t.Function.Parameters,
			})
		}
		respReq["tools"] = tools
	}

	// messages → input 数组（含 instructions 提取）
	var input []interface{}
	var systemParts []string
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			if m.Content != "" {
				systemParts = append(systemParts, m.Content)
			}
		case "assistant":
			if len(m.ToolCalls) > 0 {
				// 工具调用转为 function_call 输入项
				for _, tc := range m.ToolCalls {
					input = append(input, map[string]interface{}{
						"type":      "function_call",
						"call_id":   tc.ID,
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					})
				}
			}
			if m.Content != "" {
				input = append(input, map[string]interface{}{
					"type":    "message",
					"role":    "assistant",
					"content": []map[string]interface{}{{"type": "output_text", "text": m.Content}},
				})
			}
		case "tool":
			input = append(input, map[string]interface{}{
				"type":      "function_call_output",
				"call_id":   m.ToolCallID,
				"output":    m.Content,
			})
		default: // user
			input = append(input, map[string]interface{}{
				"type":    "message",
				"role":    "user",
				"content": []map[string]interface{}{{"type": "input_text", "text": m.Content}},
			})
		}
	}

	if len(systemParts) > 0 {
		respReq["instructions"] = strings.Join(systemParts, "\n\n")
	}
	respReq["input"] = input

	return json.Marshal(respReq)
}

// responsesOutput Responses 响应中的 output 条目
type responsesOutput struct {
	Type      string `json:"type"`
	Role      string `json:"role"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ConvertResponse 将 Responses API 响应转换为 OpenAI 规范格式（Chat Completions）
func (a *ResponsesAdapter) ConvertResponse(body []byte) ([]byte, error) {
	var resp struct {
		ID      string            `json:"id"`
		Model   string            `json:"model"`
		Status  string            `json:"status"`
		Output  []responsesOutput `json:"output"`
		Usage   struct {
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
			TotalTokens      int `json:"total_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return body, nil
	}

	msg := OpenAIMessage{Role: "assistant", Content: ""}
	var toolCalls []openAIToolCall
	finishReason := "stop"

	for _, o := range resp.Output {
		switch o.Type {
		case "message":
			if o.Role == "assistant" {
				for _, c := range o.Content {
					if c.Type == "output_text" {
						msg.Content += c.Text
					}
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, openAIToolCall{
				ID:   o.CallID,
				Type: "function",
				Function: openAIFunction{
					Name:      o.Name,
					Arguments: o.Arguments,
				},
			})
		}
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
		finishReason = "tool_calls"
	}
	if resp.Status == "incomplete" || resp.Status == "failed" {
		finishReason = "length"
	}

	openAIResp := OpenAIResponse{
		ID:      "chatcmpl-" + resp.ID,
		Object:  "chat.completion",
		Model:   resp.Model,
		Choices: []OpenAIChoice{
			{Index: 0, Message: msg, FinishReason: finishReason},
		},
		Usage: Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
			CacheHitTokens:   resp.Usage.InputTokensDetails.CachedTokens,
		},
	}
	return json.Marshal(openAIResp)
}

// ExtractUsage 从 Responses API 响应中提取用量
func (a *ResponsesAdapter) ExtractUsage(body []byte) (*Usage, error) {
	// 尝试从 OpenAI 格式提取（已 ConvertResponse 转换）
	var resp OpenAIResponse
	if err := json.Unmarshal(body, &resp); err == nil && resp.Usage.TotalTokens > 0 {
		return &resp.Usage, nil
	}

	// 尝试从 Responses 原始格式提取
	var raw struct {
		Usage struct {
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
			TotalTokens      int `json:"total_tokens"`
			InputTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &raw); err == nil && raw.Usage.TotalTokens > 0 {
		return &Usage{
			PromptTokens:     raw.Usage.InputTokens,
			CompletionTokens: raw.Usage.OutputTokens,
			TotalTokens:      raw.Usage.TotalTokens,
			CacheHitTokens:   raw.Usage.InputTokensDetails.CachedTokens,
		}, nil
	}

	return nil, errNoUsage
}

// NewStreamConverter 将 Responses API 上游 SSE 流转换为 OpenAI 规范格式 SSE
func (a *ResponsesAdapter) NewStreamConverter() StreamConverter {
	return &responsesUpstreamStreamConverter{}
}

// responsesUpstreamStreamConverter Responses SSE → OpenAI 规范 SSE
// Responses 事件流：
//   event: response.created / response.output_item.added / response.content_part.added /
//          response.output_text.delta / response.output_text.done /
//          response.function_call_arguments.delta / response.completed ...
//   data: {"type":"response.output_text.delta","delta":"text", ...}
type responsesUpstreamStreamConverter struct {
	started      bool
	finished     bool
	model        string
	responseID   string
	promptTokens int
	outputTokens int
	finishReason string
	toolSeq      int // function_call 序号
	toolStarted  map[string]bool
}

func (c *responsesUpstreamStreamConverter) Convert(line []byte) []byte {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data: ")) {
		return nil
	}
	payload := bytes.TrimSpace(trimmed[len("data: "):])
	if len(payload) == 0 {
		return nil
	}

	var evt struct {
		Type     string          `json:"type"`
		Delta    string          `json:"delta"`
		Item     json.RawMessage `json:"item"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		return nil
	}

	var out bytes.Buffer

	switch evt.Type {
	case "response.created":
		var r struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		}
		_ = json.Unmarshal(evt.Response, &r)
		c.responseID = r.ID
		c.model = r.Model

	case "response.output_item.added":
		var item struct {
			Type   string `json:"type"`
			ID     string `json:"id"`
			Name   string `json:"name"`
			CallID string `json:"call_id"`
		}
		_ = json.Unmarshal(evt.Item, &item)
		if item.Type == "function_call" {
			if c.toolStarted == nil {
				c.toolStarted = make(map[string]bool)
			}
			if !c.toolStarted[item.ID] {
				c.toolStarted[item.ID] = true
				if c.started {
					c.emitToolCallStartChunk(&out, c.toolSeq, item.ID, item.Name)
				}
				c.toolSeq++
			}
		}

	case "response.output_text.delta":
		if !c.started {
			c.emitRoleChunk(&out)
			c.started = true
		}
		if evt.Delta != "" {
			c.emitContentChunk(&out, evt.Delta)
		}

	case "response.function_call_arguments.delta":
		if !c.started {
			c.emitRoleChunk(&out)
			c.started = true
		}
		if evt.Delta != "" {
			c.emitToolCallArgsChunk(&out, c.toolSeq-1, evt.Delta)
		}

	case "response.completed":
		var r struct {
			Status string `json:"status"`
			Usage  struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		_ = json.Unmarshal(evt.Response, &r)
		if r.Status == "completed" {
			c.finishReason = "stop"
		} else {
			c.finishReason = "length"
		}
		c.promptTokens = r.Usage.InputTokens
		c.outputTokens = r.Usage.OutputTokens
	}

	if out.Len() > 0 {
		return out.Bytes()
	}
	return nil
}

func (c *responsesUpstreamStreamConverter) Finish() []byte {
	if c.finished {
		return nil
	}
	c.finished = true
	var out bytes.Buffer
	if !c.started {
		c.emitRoleChunk(&out)
	}
	reason := c.finishReason
	if reason == "" {
		reason = "stop"
	}
	finish := map[string]interface{}{
		"id":      "chatcmpl-" + c.responseID,
		"object":  "chat.completion.chunk",
		"model":   c.model,
		"choices": []interface{}{map[string]interface{}{
			"index":         0,
			"delta":         map[string]interface{}{},
			"finish_reason": reason,
		}},
		"usage": map[string]interface{}{
			"prompt_tokens":     c.promptTokens,
			"completion_tokens": c.outputTokens,
			"total_tokens":      c.promptTokens + c.outputTokens,
		},
	}
	data, _ := json.Marshal(finish)
	out.WriteString("data: " + string(data) + "\n\n")
	out.WriteString("data: [DONE]\n\n")
	return out.Bytes()
}

func (c *responsesUpstreamStreamConverter) emitRoleChunk(out *bytes.Buffer) {
	c.started = true
	chunk := map[string]interface{}{
		"id":      "chatcmpl-" + c.responseID,
		"object":  "chat.completion.chunk",
		"model":   c.model,
		"choices": []interface{}{map[string]interface{}{
			"index": 0,
			"delta": map[string]interface{}{"role": "assistant"},
			"finish_reason": nil,
		}},
	}
	data, _ := json.Marshal(chunk)
	out.WriteString("data: " + string(data) + "\n\n")
}

func (c *responsesUpstreamStreamConverter) emitContentChunk(out *bytes.Buffer, text string) {
	chunk := map[string]interface{}{
		"id":      "chatcmpl-" + c.responseID,
		"object":  "chat.completion.chunk",
		"model":   c.model,
		"choices": []interface{}{map[string]interface{}{
			"index": 0,
			"delta": map[string]interface{}{"content": text},
			"finish_reason": nil,
		}},
	}
	data, _ := json.Marshal(chunk)
	out.WriteString("data: " + string(data) + "\n\n")
}

func (c *responsesUpstreamStreamConverter) emitToolCallStartChunk(out *bytes.Buffer, index int, id, name string) {
	chunk := map[string]interface{}{
		"id":      "chatcmpl-" + c.responseID,
		"object":  "chat.completion.chunk",
		"model":   c.model,
		"choices": []interface{}{map[string]interface{}{
			"index": 0,
			"delta": map[string]interface{}{"tool_calls": []interface{}{map[string]interface{}{
				"index": index,
				"id":    id,
				"type":  "function",
				"function": map[string]interface{}{"name": name, "arguments": ""},
			}}},
			"finish_reason": nil,
		}},
	}
	data, _ := json.Marshal(chunk)
	out.WriteString("data: " + string(data) + "\n\n")
}

func (c *responsesUpstreamStreamConverter) emitToolCallArgsChunk(out *bytes.Buffer, index int, args string) {
	chunk := map[string]interface{}{
		"id":      "chatcmpl-" + c.responseID,
		"object":  "chat.completion.chunk",
		"model":   c.model,
		"choices": []interface{}{map[string]interface{}{
			"index": 0,
			"delta": map[string]interface{}{"tool_calls": []interface{}{map[string]interface{}{
				"index":    index,
				"function": map[string]interface{}{"arguments": args},
			}}},
			"finish_reason": nil,
		}},
	}
	data, _ := json.Marshal(chunk)
	out.WriteString("data: " + string(data) + "\n\n")
}
