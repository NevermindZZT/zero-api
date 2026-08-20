package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// AnthropicDownstreamAdapter Anthropic Messages API 下游适配器
// 客户端以 Anthropic 协议调用 zero-api（POST /v1/messages），
// 内部转换为 OpenAI 规范格式处理，响应再转回 Anthropic 格式。
type AnthropicDownstreamAdapter struct{}

func (a *AnthropicDownstreamAdapter) Protocol() string    { return "anthropic" }
func (a *AnthropicDownstreamAdapter) IsPassthrough() bool { return false }

// ===== 请求转换：Anthropic → OpenAI 规范格式 =====

// anthropicDownMessage Anthropic 消息（content 可为字符串或内容块数组）
type anthropicDownMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicDownContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

type anthropicDownTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicDownRequest struct {
	Model         string                 `json:"model"`
	MaxTokens     int                    `json:"max_tokens"`
	System        json.RawMessage        `json:"system,omitempty"`
	Messages      []anthropicDownMessage `json:"messages"`
	Stream        bool                   `json:"stream,omitempty"`
	Temperature   *float64               `json:"temperature,omitempty"`
	TopP          *float64               `json:"top_p,omitempty"`
	TopK          int                    `json:"top_k,omitempty"`
	StopSequences []string               `json:"stop_sequences,omitempty"`
	Tools         []anthropicDownTool    `json:"tools,omitempty"`
}

// openAICanonicalMessage OpenAI 规范消息
type openAICanonicalMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string            `json:"type"`
	Function openAIFunctionDef `json:"function"`
}

type openAIFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openAICanonicalRequest struct {
	Model              string                   `json:"model"`
	PreviousResponseID string                   `json:"previous_response_id,omitempty"`
	Messages           []openAICanonicalMessage `json:"messages"`
	MaxTokens          int                      `json:"max_tokens,omitempty"`
	Temperature        *float64                 `json:"temperature,omitempty"`
	TopP               *float64                 `json:"top_p,omitempty"`
	Stop               interface{}              `json:"stop,omitempty"`
	Stream             bool                     `json:"stream,omitempty"`
	Tools              []openAITool             `json:"tools,omitempty"`
}

func (a *AnthropicDownstreamAdapter) RequestToCanonical(body []byte) ([]byte, error) {
	var req anthropicDownRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("解析 Anthropic 请求失败: %w", err)
	}
	if req.Model == "" {
		return nil, fmt.Errorf("请求缺少 model 字段")
	}

	canonical := openAICanonicalRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}

	// system：字符串或内容块数组 → 首条 system 消息
	if len(req.System) > 0 && string(req.System) != "null" {
		var sysStr string
		var sysBlocks []anthropicDownContentBlock
		if err := json.Unmarshal(req.System, &sysBlocks); err == nil {
			var sb strings.Builder
			for _, b := range sysBlocks {
				if b.Type == "text" {
					sb.WriteString(b.Text)
				}
			}
			sysStr = sb.String()
		} else if err := json.Unmarshal(req.System, &sysStr); err == nil {
			// 字符串形式
		}
		if sysStr != "" {
			canonical.Messages = append(canonical.Messages, openAICanonicalMessage{
				Role:    "system",
				Content: sysStr,
			})
		}
	}

	// stop_sequences → stop
	if len(req.StopSequences) > 0 {
		if len(req.StopSequences) == 1 {
			canonical.Stop = req.StopSequences[0]
		} else {
			canonical.Stop = req.StopSequences
		}
	}

	// tools → OpenAI function tools
	for _, t := range req.Tools {
		canonical.Tools = append(canonical.Tools, openAITool{
			Type: "function",
			Function: openAIFunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	// messages
	for _, m := range req.Messages {
		msg, err := convertAnthropicMessage(m)
		if err != nil {
			return nil, err
		}
		canonical.Messages = append(canonical.Messages, msg)
	}

	return json.Marshal(canonical)
}

// convertAnthropicMessage 将单条 Anthropic 消息转换为 OpenAI 规范消息
// content 可为字符串或内容块数组（text / tool_use / tool_result / image）
func convertAnthropicMessage(m anthropicDownMessage) (openAICanonicalMessage, error) {
	// 字符串形式
	var str string
	if err := json.Unmarshal(m.Content, &str); err == nil {
		return openAICanonicalMessage{Role: normalizeRole(m.Role), Content: str}, nil
	}

	// 内容块数组形式
	var blocks []anthropicDownContentBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return openAICanonicalMessage{}, fmt.Errorf("无法解析消息 content: %w", err)
	}

	var texts []string
	var toolCalls []openAIToolCall
	var toolResult *anthropicDownContentBlock

	for _, b := range blocks {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "tool_use":
			// 转为 assistant 消息的 tool_calls
			input := b.Input
			if len(input) == 0 || string(input) == "null" {
				input = []byte("{}")
			}
			toolCalls = append(toolCalls, openAIToolCall{
				ID:   b.ID,
				Type: "function",
				Function: openAIFunction{
					Name:      b.Name,
					Arguments: string(input),
				},
			})
		case "tool_result":
			// 转为 tool 角色的消息（在循环后单独处理）
			tr := b
			toolResult = &tr
		case "image":
			// 第一版忽略图像块，仅保留文本
		}
	}

	// tool_result 消息
	if toolResult != nil {
		content := ""
		var contentStr string
		if err := json.Unmarshal(toolResult.Content, &contentStr); err == nil {
			content = contentStr
		} else {
			var contentBlocks []anthropicDownContentBlock
			if err := json.Unmarshal(toolResult.Content, &contentBlocks); err == nil {
				var sb strings.Builder
				for _, cb := range contentBlocks {
					if cb.Type == "text" {
						sb.WriteString(cb.Text)
					}
				}
				content = sb.String()
			}
		}
		return openAICanonicalMessage{
			Role:       "tool",
			Content:    content,
			ToolCallID: toolResult.ToolUseID,
		}, nil
	}

	// assistant 消息带 tool_calls
	if len(toolCalls) > 0 {
		return openAICanonicalMessage{
			Role:      "assistant",
			Content:   strings.Join(texts, ""),
			ToolCalls: toolCalls,
		}, nil
	}

	return openAICanonicalMessage{
		Role:    normalizeRole(m.Role),
		Content: strings.Join(texts, ""),
	}, nil
}

// normalizeRole 将 Anthropic 角色映射为 OpenAI 角色
func normalizeRole(role string) string {
	switch role {
	case "assistant", "user":
		return role
	default:
		return "user"
	}
}

// ===== 响应转换：OpenAI 规范格式 → Anthropic =====

type openAIRespUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIRespChoice struct {
	Index        int                    `json:"index"`
	Message      openAICanonicalMessage `json:"message"`
	FinishReason string                 `json:"finish_reason"`
}

type openAICanonicalResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []openAIRespChoice `json:"choices"`
	Usage   openAIRespUsage    `json:"usage"`
}

type anthropicDownResponse struct {
	ID           string                      `json:"id"`
	Type         string                      `json:"type"`
	Role         string                      `json:"role"`
	Model        string                      `json:"model"`
	Content      []anthropicDownContentBlock `json:"content"`
	StopReason   string                      `json:"stop_reason"`
	StopSequence *string                     `json:"stop_sequence"`
	Usage        anthropicDownUsage          `json:"usage"`
}

type anthropicDownUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (a *AnthropicDownstreamAdapter) ResponseToDownstream(body []byte) ([]byte, error) {
	var resp openAICanonicalResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return body, nil // 无法解析则透传
	}
	if len(resp.Choices) == 0 {
		return body, nil
	}

	msg := resp.Choices[0].Message
	downResp := anthropicDownResponse{
		ID:         "msg_" + strings.TrimPrefix(resp.ID, "chatcmpl-"),
		Type:       "message",
		Role:       "assistant",
		Model:      resp.Model,
		Content:    []anthropicDownContentBlock{},
		StopReason: mapStopReason(resp.Choices[0].FinishReason),
		Usage: anthropicDownUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}

	// text 内容块
	if msg.Content != "" {
		downResp.Content = append(downResp.Content, anthropicDownContentBlock{
			Type: "text",
			Text: msg.Content,
		})
	}

	// tool_calls → tool_use 内容块
	for _, tc := range msg.ToolCalls {
		downResp.Content = append(downResp.Content, anthropicDownContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: json.RawMessage(tc.Function.Arguments),
		})
	}

	return json.Marshal(downResp)
}

// mapStopReason OpenAI finish_reason → Anthropic stop_reason
func mapStopReason(reason string) string {
	switch reason {
	case "stop", "":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return reason
	}
}

// ===== 流式转换：OpenAI SSE → Anthropic SSE =====

// openAICanonicalChunk OpenAI 流式响应块
type openAICanonicalChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type anthropicStreamConverter struct {
	started      bool // 已发送 message_start
	finished     bool // 已发送收尾事件
	textStarted  bool // text 块已发送 content_block_start
	toolStarted  map[int]bool
	blockIndex   int // 全局内容块索引
	model        string
	messageID    string
	stopReason   string
	inputTokens  int
	outputTokens int
}

func (a *AnthropicDownstreamAdapter) NewStreamConverter() StreamConverter {
	return &anthropicStreamConverter{
		toolStarted: make(map[int]bool),
	}
}

func (c *anthropicStreamConverter) Convert(line []byte) []byte {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data: ")) {
		// 忽略注释行、空行、event: 行
		return nil
	}
	payload := bytes.TrimSpace(trimmed[len("data: "):])
	if bytes.Equal(payload, []byte("[DONE]")) {
		return nil // 收尾在 Finish 统一处理
	}

	var chunk openAICanonicalChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return nil // 无法解析的事件忽略
	}

	var out bytes.Buffer

	// message_start（首个事件）
	if !c.started {
		if chunk.ID != "" {
			c.messageID = "msg_" + strings.TrimPrefix(chunk.ID, "chatcmpl-")
		} else {
			c.messageID = "msg_stream"
		}
		c.model = chunk.Model
		if chunk.Usage != nil {
			c.inputTokens = chunk.Usage.PromptTokens
		}
		writeAnthropicEvent(&out, "message_start", map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":      c.messageID,
				"type":    "message",
				"role":    "assistant",
				"model":   c.model,
				"content": []interface{}{},
				"usage": map[string]interface{}{
					"input_tokens":  c.inputTokens,
					"output_tokens": 0,
				},
			},
		})
		c.started = true
	}

	if chunk.Usage != nil {
		if chunk.Usage.PromptTokens > 0 {
			c.inputTokens = chunk.Usage.PromptTokens
		}
		if chunk.Usage.CompletionTokens > 0 {
			c.outputTokens = chunk.Usage.CompletionTokens
		}
	}

	if len(chunk.Choices) == 0 {
		if out.Len() > 0 {
			return out.Bytes()
		}
		return nil
	}

	choice := chunk.Choices[0]
	delta := choice.Delta

	// text 增量
	if delta.Content != "" {
		if !c.textStarted {
			writeAnthropicEvent(&out, "content_block_start", map[string]interface{}{
				"type":  "content_block_start",
				"index": c.blockIndex,
				"content_block": map[string]interface{}{
					"type": "text",
					"text": "",
				},
			})
			c.textStarted = true
			c.blockIndex++
		}
		writeAnthropicEvent(&out, "content_block_delta", map[string]interface{}{
			"type":  "content_block_delta",
			"index": c.blockIndex - 1,
			"delta": map[string]interface{}{
				"type": "text_delta",
				"text": delta.Content,
			},
		})
	}

	// tool_calls 增量
	for _, tc := range delta.ToolCalls {
		if !c.toolStarted[tc.Index] {
			writeAnthropicEvent(&out, "content_block_start", map[string]interface{}{
				"type":  "content_block_start",
				"index": c.blockIndex,
				"content_block": map[string]interface{}{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": map[string]interface{}{},
				},
			})
			c.toolStarted[tc.Index] = true
			c.blockIndex++
		}
		if tc.Function.Arguments != "" {
			writeAnthropicEvent(&out, "content_block_delta", map[string]interface{}{
				"type":  "content_block_delta",
				"index": c.blockIndex - 1,
				"delta": map[string]interface{}{
					"type":         "input_json_delta",
					"partial_json": tc.Function.Arguments,
				},
			})
		}
	}

	// finish_reason：记录，收尾事件在 Finish 统一补发
	if choice.FinishReason != "" {
		c.stopReason = mapStopReason(choice.FinishReason)
	}

	if out.Len() > 0 {
		return out.Bytes()
	}
	return nil
}

func (c *anthropicStreamConverter) Finish() []byte {
	if c.finished {
		return nil
	}
	c.finished = true

	var out bytes.Buffer
	// 空响应兜底
	if !c.started {
		c.messageID = "msg_stream"
		writeAnthropicEvent(&out, "message_start", map[string]interface{}{
			"type": "message_start",
			"message": map[string]interface{}{
				"id":      c.messageID,
				"type":    "message",
				"role":    "assistant",
				"model":   "",
				"content": []interface{}{},
				"usage": map[string]interface{}{
					"input_tokens":  0,
					"output_tokens": 0,
				},
			},
		})
		c.started = true
	}

	// 结束已开始的内容块
	if c.textStarted || len(c.toolStarted) > 0 {
		for i := 0; i < c.blockIndex; i++ {
			writeAnthropicEvent(&out, "content_block_stop", map[string]interface{}{
				"type":  "content_block_stop",
				"index": i,
			})
		}
	}

	if c.stopReason == "" {
		c.stopReason = "end_turn"
	}
	writeAnthropicEvent(&out, "message_delta", map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   c.stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]interface{}{
			"output_tokens": c.outputTokens,
		},
	})
	writeAnthropicEvent(&out, "message_stop", map[string]interface{}{
		"type": "message_stop",
	})

	return out.Bytes()
}

// writeAnthropicEvent 写出 Anthropic SSE 事件（event: + data:）
func writeAnthropicEvent(buf *bytes.Buffer, event string, payload map[string]interface{}) {
	data, _ := json.Marshal(payload)
	buf.WriteString("event: " + event + "\n")
	buf.WriteString("data: " + string(data) + "\n\n")
}
