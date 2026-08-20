package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ResponsesDownstreamAdapter OpenAI Responses API 下游适配器
// 客户端以 Responses 协议调用 zero-api（POST /v1/responses），
// 内部转换为 OpenAI 规范格式（Chat Completions）处理，响应再转回 Responses 格式。
type ResponsesDownstreamAdapter struct{}

func (a *ResponsesDownstreamAdapter) Protocol() string    { return "responses" }
func (a *ResponsesDownstreamAdapter) IsPassthrough() bool { return false }

// ===== 请求转换：Responses → OpenAI 规范格式 =====

type responsesDownInput struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Output    string          `json:"output"`
	ItemID    string          `json:"item_id"`
	Type2     string          `json:"type_2,omitempty"` // 占位避免误用
}

type responsesDownRequest struct {
	Model              string                 `json:"model"`
	PreviousResponseID string                 `json:"previous_response_id"`
	Instructions       string                 `json:"instructions"`
	Input              json.RawMessage        `json:"input"`
	Stream             bool                   `json:"stream"`
	MaxOutputTokens    int                    `json:"max_output_tokens"`
	Temperature        *float64               `json:"temperature"`
	TopP               *float64               `json:"top_p"`
	Stop               []string               `json:"stop"`
	Tools              []responsesDownTool    `json:"tools"`
	ToolChoice         string                 `json:"tool_choice"`
	Reasoning          map[string]interface{} `json:"reasoning"`
}

type responsesDownTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

func (a *ResponsesDownstreamAdapter) RequestToCanonical(body []byte) ([]byte, error) {
	var req responsesDownRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("解析 Responses 请求失败: %w", err)
	}
	if req.Model == "" {
		return nil, fmt.Errorf("请求缺少 model 字段")
	}

	canonical := openAICanonicalRequest{
		Model:              req.Model,
		PreviousResponseID: req.PreviousResponseID,
		Stream:             req.Stream,
		Temperature:        req.Temperature,
		TopP:               req.TopP,
	}

	// max_output_tokens → max_tokens
	if req.MaxOutputTokens > 0 {
		canonical.MaxTokens = req.MaxOutputTokens
	}

	// stop 数组 → stop
	if len(req.Stop) > 0 {
		if len(req.Stop) == 1 {
			canonical.Stop = req.Stop[0]
		} else {
			canonical.Stop = req.Stop
		}
	}

	// instructions → system 消息
	if req.Instructions != "" {
		canonical.Messages = append(canonical.Messages, openAICanonicalMessage{
			Role:    "system",
			Content: req.Instructions,
		})
	}

	// tools → OpenAI function tools
	for _, t := range req.Tools {
		canonical.Tools = append(canonical.Tools, openAITool{
			Type: "function",
			Function: openAIFunctionDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	// input → messages
	msgs, err := convertResponsesInput(req.Input)
	if err != nil {
		return nil, err
	}
	canonical.Messages = append(canonical.Messages, msgs...)

	return json.Marshal(canonical)
}

// convertResponsesInput 将 Responses input 数组转换为 OpenAI 规范消息列表
// input 支持：
//   - 字符串（单个 user 文本）
//   - {"role":"user","content":"text"}（简化形式）
//   - {"type":"message","role":"user","content":[{"type":"input_text","text":"..."}]}
//   - {"type":"function_call","call_id":"...","name":"...","arguments":"..."}
//   - {"type":"function_call_output","call_id":"...","output":"..."}
func convertResponsesInput(raw json.RawMessage) ([]openAICanonicalMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	// 字符串形式
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return []openAICanonicalMessage{{Role: "user", Content: str}}, nil
	}

	// 对象形式（单条）
	var single map[string]interface{}
	if err := json.Unmarshal(raw, &single); err == nil {
		msgs, err := convertResponsesInputItem(single)
		if err != nil {
			return nil, err
		}
		if msgs != nil {
			return []openAICanonicalMessage{*msgs}, nil
		}
		return nil, nil
	}

	// 数组形式
	var items []map[string]interface{}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("无法解析 input: %w", err)
	}

	var msgs []openAICanonicalMessage
	for _, item := range items {
		m, err := convertResponsesInputItem(item)
		if err != nil {
			return nil, err
		}
		if m != nil {
			msgs = append(msgs, *m)
		}
	}
	return msgs, nil
}

func convertResponsesInputItem(item map[string]interface{}) (*openAICanonicalMessage, error) {
	typ, _ := item["type"].(string)
	role, _ := item["role"].(string)

	switch typ {
	case "", "message":
		// 简化形式 {"role":"user","content":"text"} 或 {"type":"message",...}
		if role == "" {
			role = "user"
		}
		content := ""
		switch c := item["content"].(type) {
		case string:
			content = c
		case []interface{}:
			// content 块数组：input_text / input_image
			var texts []string
			for _, blk := range c {
				if bm, ok := blk.(map[string]interface{}); ok {
					if bt, _ := bm["type"].(string); bt == "input_text" || bt == "output_text" || bt == "text" {
						if t, ok := bm["text"].(string); ok {
							texts = append(texts, t)
						}
					}
				}
			}
			content = strings.Join(texts, "")
		}
		// 首条消息如果 role 是 system 保持，否则归入 user
		msgRole := normalizeRole(role)
		if role == "system" {
			msgRole = "system"
		}
		return &openAICanonicalMessage{Role: msgRole, Content: content}, nil

	case "function_call":
		callID, _ := item["call_id"].(string)
		name, _ := item["name"].(string)
		args, _ := item["arguments"].(string)
		return &openAICanonicalMessage{
			Role:    "assistant",
			Content: "",
			ToolCalls: []openAIToolCall{{
				ID:   callID,
				Type: "function",
				Function: openAIFunction{
					Name:      name,
					Arguments: args,
				},
			}},
		}, nil

	case "function_call_output":
		callID, _ := item["call_id"].(string)
		output, _ := item["output"].(string)
		if outputValue, ok := item["output"]; ok {
			if outputString, ok := outputValue.(string); ok {
				output = outputString
			} else if b, err := json.Marshal(outputValue); err == nil {
				// Responses 允许 output 为字符串或内容块数组/对象。
				output = string(b)
			}
		}
		return &openAICanonicalMessage{
			Role:       "tool",
			Content:    output,
			ToolCallID: callID,
		}, nil

	default:
		// computer_call 等暂不支持，跳过
		return nil, nil
	}
}

// ===== 响应转换：OpenAI 规范格式 → Responses =====

func (a *ResponsesDownstreamAdapter) ResponseToDownstream(body []byte) ([]byte, error) {
	var resp openAICanonicalResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return body, nil
	}
	if len(resp.Choices) == 0 {
		return body, nil
	}

	msg := resp.Choices[0].Message
	status := "completed"
	finishReason := resp.Choices[0].FinishReason
	if finishReason == "length" {
		status = "incomplete"
	}

	outResp := map[string]interface{}{
		"id":         "resp_" + strings.TrimPrefix(resp.ID, "chatcmpl-"),
		"object":     "response",
		"created_at": resp.Created,
		"status":     status,
		"model":      resp.Model,
		"output":     []interface{}{},
		"usage": map[string]interface{}{
			"input_tokens":  resp.Usage.PromptTokens,
			"output_tokens": resp.Usage.CompletionTokens,
			"total_tokens":  resp.Usage.TotalTokens,
		},
	}

	var output []interface{}

	// 文本消息
	if msg.Content != "" {
		output = append(output, map[string]interface{}{
			"type": "message",
			"id":   "msg_" + strings.TrimPrefix(resp.ID, "chatcmpl-"),
			"role": "assistant",
			"content": []map[string]interface{}{
				{"type": "output_text", "text": msg.Content},
			},
		})
	}

	// 工具调用
	for i, tc := range msg.ToolCalls {
		callID := tc.ID
		if callID == "" {
			callID = fmt.Sprintf("call_%d", i)
		}
		output = append(output, map[string]interface{}{
			"type":      "function_call",
			"id":        "fc_" + callID,
			"call_id":   callID,
			"name":      tc.Function.Name,
			"arguments": tc.Function.Arguments,
			"status":    "completed",
		})
	}

	outResp["output"] = output
	return json.Marshal(outResp)
}

// ===== 流式转换：OpenAI 规范 SSE → Responses SSE =====

type responsesDownStreamConverter struct {
	started      bool
	finished     bool
	model        string
	responseID   string
	itemSeq      int
	textItemSent bool
	toolItems    map[int]bool
	toolSeq      int
	inputTokens  int
	outputTokens int
}

func (a *ResponsesDownstreamAdapter) NewStreamConverter() StreamConverter {
	return &responsesDownStreamConverter{
		toolItems: make(map[int]bool),
	}
}

func (c *responsesDownStreamConverter) Convert(line []byte) []byte {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data: ")) {
		return nil
	}
	payload := bytes.TrimSpace(trimmed[len("data: "):])
	if bytes.Equal(payload, []byte("[DONE]")) {
		return nil // 收尾在 Finish
	}

	var chunk openAICanonicalChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return nil
	}
	if chunk.Usage != nil {
		c.inputTokens = chunk.Usage.PromptTokens
		c.outputTokens = chunk.Usage.CompletionTokens
	}

	var out bytes.Buffer

	// response.created（首个事件）
	if !c.started {
		if chunk.ID != "" {
			c.responseID = "resp_" + strings.TrimPrefix(chunk.ID, "chatcmpl-")
		} else {
			c.responseID = "resp_stream"
		}
		c.model = chunk.Model
		writeResponsesEvent(&out, "response.created", map[string]interface{}{
			"type": "response.created",
			"response": map[string]interface{}{
				"id":     c.responseID,
				"object": "response",
				"status": "in_progress",
				"model":  c.model,
				"output": []interface{}{},
				"usage":  nil,
			},
		})
		c.started = true
	}

	if len(chunk.Choices) == 0 {
		if out.Len() > 0 {
			return out.Bytes()
		}
		return nil
	}

	choice := chunk.Choices[0]
	delta := choice.Delta

	// 文本增量
	if delta.Content != "" {
		if !c.textItemSent {
			// response.output_item.added + content_part.added
			c.itemSeq++
			itemID := fmt.Sprintf("msg_%s_%d", c.responseID, c.itemSeq)
			writeResponsesEvent(&out, "response.output_item.added", map[string]interface{}{
				"type":         "response.output_item.added",
				"output_index": c.itemSeq - 1,
				"item": map[string]interface{}{
					"id":      itemID,
					"type":    "message",
					"role":    "assistant",
					"status":  "in_progress",
					"content": []interface{}{},
				},
			})
			writeResponsesEvent(&out, "response.content_part.added", map[string]interface{}{
				"type":          "response.content_part.added",
				"item_id":       itemID,
				"output_index":  c.itemSeq - 1,
				"content_index": 0,
				"part": map[string]interface{}{
					"type": "output_text",
					"text": "",
				},
			})
			c.textItemSent = true
		}
		writeResponsesEvent(&out, "response.output_text.delta", map[string]interface{}{
			"type":          "response.output_text.delta",
			"item_id":       fmt.Sprintf("msg_%s_%d", c.responseID, c.itemSeq),
			"output_index":  c.itemSeq - 1,
			"content_index": 0,
			"delta":         delta.Content,
		})
	}

	// 工具调用增量
	for _, tc := range delta.ToolCalls {
		if !c.toolItems[tc.Index] {
			c.itemSeq++
			c.toolSeq++
			itemID := fmt.Sprintf("fc_%s_%d", c.responseID, c.toolSeq)
			callID := tc.ID
			if callID == "" {
				callID = fmt.Sprintf("call_%d", c.toolSeq)
			}
			c.toolItems[tc.Index] = true
			writeResponsesEvent(&out, "response.output_item.added", map[string]interface{}{
				"type":         "response.output_item.added",
				"output_index": c.itemSeq - 1,
				"item": map[string]interface{}{
					"id":        itemID,
					"type":      "function_call",
					"call_id":   callID,
					"name":      tc.Function.Name,
					"arguments": "",
					"status":    "in_progress",
				},
			})
		}
		if tc.Function.Arguments != "" {
			writeResponsesEvent(&out, "response.function_call_arguments.delta", map[string]interface{}{
				"type":         "response.function_call_arguments.delta",
				"item_id":      fmt.Sprintf("fc_%s_%d", c.responseID, c.toolSeq),
				"output_index": c.itemSeq - 1,
				"delta":        tc.Function.Arguments,
			})
		}
	}

	if out.Len() > 0 {
		return out.Bytes()
	}
	return nil
}

func (c *responsesDownStreamConverter) Finish() []byte {
	if c.finished {
		return nil
	}
	c.finished = true
	var out bytes.Buffer
	if !c.started {
		c.responseID = "resp_stream"
		writeResponsesEvent(&out, "response.created", map[string]interface{}{
			"type": "response.created",
			"response": map[string]interface{}{
				"id":     c.responseID,
				"object": "response",
				"status": "in_progress",
				"model":  "",
				"output": []interface{}{},
				"usage":  nil,
			},
		})
		c.started = true
	}

	// 结束已开始的条目
	if c.textItemSent {
		itemID := fmt.Sprintf("msg_%s_%d", c.responseID, c.itemSeq)
		writeResponsesEvent(&out, "response.output_text.done", map[string]interface{}{
			"type":          "response.output_text.done",
			"item_id":       itemID,
			"output_index":  c.itemSeq - 1,
			"content_index": 0,
			"text":          "",
		})
		writeResponsesEvent(&out, "response.content_part.done", map[string]interface{}{
			"type":          "response.content_part.done",
			"item_id":       itemID,
			"output_index":  c.itemSeq - 1,
			"content_index": 0,
			"part": map[string]interface{}{
				"type": "output_text",
				"text": "",
			},
		})
		writeResponsesEvent(&out, "response.output_item.done", map[string]interface{}{
			"type":         "response.output_item.done",
			"output_index": c.itemSeq - 1,
			"item": map[string]interface{}{
				"id":      itemID,
				"type":    "message",
				"role":    "assistant",
				"status":  "completed",
				"content": []interface{}{},
			},
		})
	}

	writeResponsesEvent(&out, "response.completed", map[string]interface{}{
		"type": "response.completed",
		"response": map[string]interface{}{
			"id":     c.responseID,
			"object": "response",
			"status": "completed",
			"model":  c.model,
			"output": []interface{}{},
			"usage": map[string]interface{}{
				"input_tokens":  c.inputTokens,
				"output_tokens": c.outputTokens,
				"total_tokens":  c.inputTokens + c.outputTokens,
			},
		},
	})

	return out.Bytes()
}

// writeResponsesEvent 写出 Responses SSE 事件（event: + data:）
func writeResponsesEvent(buf *bytes.Buffer, event string, payload map[string]interface{}) {
	data, _ := json.Marshal(payload)
	buf.WriteString("event: " + event + "\n")
	buf.WriteString("data: " + string(data) + "\n\n")
}
