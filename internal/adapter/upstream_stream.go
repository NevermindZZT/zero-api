package adapter

import (
	"bytes"
	"encoding/json"
)

// ===== Anthropic 上游 SSE → OpenAI 规范格式 SSE =====

// anthropicUpstreamStreamConverter 将 Anthropic 上游 SSE 事件转换为 OpenAI 规范格式 SSE chunk
// Anthropic 事件流：
//   event: message_start / content_block_start / content_block_delta / content_block_stop /
//          message_delta / message_stop / ping
//   data: {...}
type anthropicUpstreamStreamConverter struct {
	started       bool   // 已发出首 chunk（role）
	finished      bool   // 已发出 [DONE]
	model         string
	messageID     string
	promptTokens  int
	outputTokens  int
	stopReason    string
	blockIndex    int // 当前内容块索引
	textBlockOpen bool
	toolBlocks    map[int]bool
	toolBlockSeq  int // 已开始的 tool_use 块序号（映射到 OpenAI tool_calls index）
	toolCallSeq   int // OpenAI tool_calls index 递增
}

func (c *anthropicUpstreamStreamConverter) Convert(line []byte) []byte {
	trimmed := bytes.TrimSpace(line)
	// data: 行才是有效载荷；event: 行和空行忽略
	if !bytes.HasPrefix(trimmed, []byte("data: ")) {
		return nil
	}
	payload := bytes.TrimSpace(trimmed[len("data: "):])
	if len(payload) == 0 {
		return nil
	}

	var evt struct {
		Type        string          `json:"type"`
		Index       int             `json:"index"`
		Message     json.RawMessage `json:"message"`
		ContentBlock json.RawMessage `json:"content_block"`
		Delta       json.RawMessage `json:"delta"`
		Usage       json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(payload, &evt); err != nil {
		return nil
	}

	var out bytes.Buffer

	switch evt.Type {
	case "message_start":
		var msg struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage struct {
				InputTokens int `json:"input_tokens"`
			} `json:"usage"`
		}
		_ = json.Unmarshal(evt.Message, &msg)
		c.messageID = msg.ID
		c.model = msg.Model
		c.promptTokens = msg.Usage.InputTokens
		c.emitRoleChunk(&out)

	case "content_block_start":
		var block struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal(evt.ContentBlock, &block)
		c.blockIndex = evt.Index
		switch block.Type {
		case "text":
			c.textBlockOpen = true
		case "tool_use":
			if c.toolBlocks == nil {
				c.toolBlocks = make(map[int]bool)
			}
			c.toolBlocks[evt.Index] = true
			c.emitToolCallStartChunk(&out, c.toolCallSeq, block.ID, block.Name)
			c.toolCallSeq++
		}

	case "content_block_delta":
		var delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		}
		_ = json.Unmarshal(evt.Delta, &delta)
		switch delta.Type {
		case "text_delta":
			if delta.Text != "" {
				c.emitContentChunk(&out, delta.Text)
			}
		case "input_json_delta":
			if delta.PartialJSON != "" {
				c.emitToolCallArgsChunk(&out, c.toolCallSeq-1, delta.PartialJSON)
			}
		}

	case "message_delta":
		var delta struct {
			StopReason string `json:"stop_reason"`
		}
		var usage struct {
			OutputTokens int `json:"output_tokens"`
		}
		_ = json.Unmarshal(evt.Delta, &delta)
		_ = json.Unmarshal(evt.Usage, &usage)
		c.stopReason = delta.StopReason
		if usage.OutputTokens > 0 {
			c.outputTokens = usage.OutputTokens
		}

	case "message_stop":
		// 收尾在 Finish 统一处理
	}

	if out.Len() > 0 {
		return out.Bytes()
	}
	return nil
}

func (c *anthropicUpstreamStreamConverter) Finish() []byte {
	if c.finished {
		return nil
	}
	c.finished = true
	var out bytes.Buffer
	// 若从未发出首 chunk（空响应），补一个
	if !c.started {
		c.emitRoleChunk(&out)
	}
	// finish chunk（含 usage）
	reason := c.stopReason
	if reason == "" {
		reason = "stop"
	}
	finish := map[string]interface{}{
		"id":      "chatcmpl-" + c.messageID,
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

func (c *anthropicUpstreamStreamConverter) emitRoleChunk(out *bytes.Buffer) {
	c.started = true
	chunk := map[string]interface{}{
		"id":      "chatcmpl-" + c.messageID,
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

func (c *anthropicUpstreamStreamConverter) emitContentChunk(out *bytes.Buffer, text string) {
	chunk := map[string]interface{}{
		"id":      "chatcmpl-" + c.messageID,
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

func (c *anthropicUpstreamStreamConverter) emitToolCallStartChunk(out *bytes.Buffer, index int, id, name string) {
	chunk := map[string]interface{}{
		"id":      "chatcmpl-" + c.messageID,
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

func (c *anthropicUpstreamStreamConverter) emitToolCallArgsChunk(out *bytes.Buffer, index int, args string) {
	chunk := map[string]interface{}{
		"id":      "chatcmpl-" + c.messageID,
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

// ===== Gemini 上游 SSE → OpenAI 规范格式 SSE =====

// geminiUpstreamStreamConverter 将 Gemini 上游 SSE 流转换为 OpenAI 规范格式 SSE
// Gemini 流式响应格式（无 event: 行）：
//   data: {"candidates":[{"content":{"parts":[{"text":"..."}]},"finishReason":"STOP"}],
//          "usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}
type geminiUpstreamStreamConverter struct {
	started      bool
	finished     bool
	promptTokens int
	outputTokens int
	finishReason string
}

func (c *geminiUpstreamStreamConverter) Convert(line []byte) []byte {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data: ")) {
		return nil
	}
	payload := bytes.TrimSpace(trimmed[len("data: "):])
	if len(payload) == 0 {
		return nil
	}

	var resp GeminiResponse
	if err := json.Unmarshal(payload, &resp); err != nil {
		return nil
	}

	if resp.UsageMetadata.TotalTokenCount > 0 {
		c.promptTokens = resp.UsageMetadata.PromptTokenCount
		c.outputTokens = resp.UsageMetadata.CandidatesTokenCount
	}

	content := ""
	if len(resp.Candidates) > 0 {
		if len(resp.Candidates[0].Content.Parts) > 0 {
			content = resp.Candidates[0].Content.Parts[0].Text
		}
		if resp.Candidates[0].FinishReason != "" {
			c.finishReason = resp.Candidates[0].FinishReason
		}
	}

	if content == "" {
		return nil
	}

	var out bytes.Buffer
	if !c.started {
		c.started = true
		roleChunk := map[string]interface{}{
			"id":      "chatcmpl-gemini",
			"object":  "chat.completion.chunk",
			"model":   "gemini",
			"choices": []interface{}{map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{"role": "assistant"},
				"finish_reason": nil,
			}},
		}
		data, _ := json.Marshal(roleChunk)
		out.WriteString("data: " + string(data) + "\n\n")
	}

	contentChunk := map[string]interface{}{
		"id":      "chatcmpl-gemini",
		"object":  "chat.completion.chunk",
		"model":   "gemini",
		"choices": []interface{}{map[string]interface{}{
			"index": 0,
			"delta": map[string]interface{}{"content": content},
			"finish_reason": nil,
		}},
	}
	data, _ := json.Marshal(contentChunk)
	out.WriteString("data: " + string(data) + "\n\n")

	return out.Bytes()
}

func (c *geminiUpstreamStreamConverter) Finish() []byte {
	if c.finished {
		return nil
	}
	c.finished = true
	var out bytes.Buffer
	if !c.started {
		c.started = true
		roleChunk := map[string]interface{}{
			"id":      "chatcmpl-gemini",
			"object":  "chat.completion.chunk",
			"model":   "gemini",
			"choices": []interface{}{map[string]interface{}{
				"index": 0,
				"delta": map[string]interface{}{"role": "assistant"},
				"finish_reason": nil,
			}},
		}
		data, _ := json.Marshal(roleChunk)
		out.WriteString("data: " + string(data) + "\n\n")
	}

	reason := c.finishReason
	switch reason {
	case "STOP":
		reason = "stop"
	case "MAX_TOKENS":
		reason = "length"
	case "":
		reason = "stop"
	}

	finish := map[string]interface{}{
		"id":      "chatcmpl-gemini",
		"object":  "chat.completion.chunk",
		"model":   "gemini",
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
