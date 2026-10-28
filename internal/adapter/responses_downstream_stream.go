package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type responsesDownStreamItem struct {
	kind, id, callID, name, arguments, text string
	index                                   int
	added                                   bool
}

type responsesDownStreamConverter struct {
	started, finished, terminal, failed bool
	model, responseID, finishReason     string
	sequence                            int
	items                               []*responsesDownStreamItem
	textItem                            *responsesDownStreamItem
	toolItems                           map[int]*responsesDownStreamItem
	inputTokens, outputTokens           int
}

func (a *ResponsesDownstreamAdapter) NewStreamConverter() StreamConverter {
	return &responsesDownStreamConverter{toolItems: make(map[int]*responsesDownStreamItem)}
}

func (c *responsesDownStreamConverter) event(out *bytes.Buffer, typ string, payload map[string]interface{}) {
	payload["type"] = typ
	payload["sequence_number"] = c.sequence
	c.sequence++
	writeResponsesEvent(out, typ, payload)
}

func (c *responsesDownStreamConverter) start(out *bytes.Buffer, id, model string) {
	if c.started {
		return
	}
	c.responseID = "resp_" + strings.TrimPrefix(id, "chatcmpl-")
	if id == "" {
		c.responseID = "resp_stream"
	}
	c.model = model
	c.event(out, "response.created", map[string]interface{}{"response": map[string]interface{}{
		"id": c.responseID, "object": "response", "status": "in_progress", "model": c.model, "output": []interface{}{}, "usage": nil,
	}})
	c.started = true
}

func (c *responsesDownStreamConverter) itemValue(item *responsesDownStreamItem, status string) map[string]interface{} {
	if item.kind == "function_call" {
		return map[string]interface{}{"id": item.id, "type": item.kind, "call_id": item.callID, "name": item.name, "arguments": item.arguments, "status": status}
	}
	return map[string]interface{}{"id": item.id, "type": "message", "role": "assistant", "status": status,
		"content": []interface{}{map[string]interface{}{"type": "output_text", "text": item.text, "annotations": []interface{}{}}}}
}

func (c *responsesDownStreamConverter) fail(out *bytes.Buffer) {
	c.failed = true
	c.event(out, "error", map[string]interface{}{"error": map[string]string{"type": "upstream_error", "message": "Upstream stream failed or tool identity is missing"}})
}

func (c *responsesDownStreamConverter) Convert(line []byte) []byte {
	if c.finished || c.failed {
		return nil
	}
	payload := responsesSSEPayload(line)
	if len(payload) == 0 {
		return nil
	}
	if bytes.Equal(payload, []byte("[DONE]")) {
		c.terminal = true
		return nil
	}
	var raw struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(payload, &raw) != nil {
		return nil
	}
	var out bytes.Buffer
	if len(raw.Error) > 0 && string(raw.Error) != "null" {
		c.fail(&out)
		return out.Bytes()
	}
	var chunk openAICanonicalChunk
	if json.Unmarshal(payload, &chunk) != nil {
		return nil
	}
	if chunk.Usage != nil {
		c.inputTokens, c.outputTokens = chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens
	}
	c.start(&out, chunk.ID, chunk.Model)
	if len(chunk.Choices) == 0 {
		return out.Bytes()
	}
	choice := chunk.Choices[0]
	if choice.FinishReason != "" {
		c.terminal = true
		c.finishReason = choice.FinishReason
		if choice.FinishReason == "error" {
			c.fail(&out)
			return out.Bytes()
		}
	}
	if choice.Delta.Content != "" {
		if c.textItem == nil {
			item := &responsesDownStreamItem{kind: "message", index: len(c.items), id: fmt.Sprintf("msg_%s_%d", c.responseID, len(c.items)), added: true}
			c.items = append(c.items, item)
			c.textItem = item
			c.event(&out, "response.output_item.added", map[string]interface{}{"output_index": item.index, "item": map[string]interface{}{
				"id": item.id, "type": "message", "role": "assistant", "status": "in_progress", "content": []interface{}{},
			}})
			c.event(&out, "response.content_part.added", map[string]interface{}{"item_id": item.id, "output_index": item.index, "content_index": 0, "part": map[string]interface{}{"type": "output_text", "text": "", "annotations": []interface{}{}}})
		}
		item := c.textItem
		item.text += choice.Delta.Content
		c.event(&out, "response.output_text.delta", map[string]interface{}{"item_id": item.id, "output_index": item.index, "content_index": 0, "delta": choice.Delta.Content})
	}
	for _, tc := range choice.Delta.ToolCalls {
		item := c.toolItems[tc.Index]
		if item == nil {
			item = &responsesDownStreamItem{kind: "function_call", index: len(c.items), id: fmt.Sprintf("fc_%s_%d", c.responseID, len(c.items))}
			c.items = append(c.items, item)
			c.toolItems[tc.Index] = item
		}
		if tc.ID != "" {
			item.callID = tc.ID
		}
		item.name += tc.Function.Name
		item.arguments += tc.Function.Arguments
		if !item.added && item.callID != "" && item.name != "" {
			item.added = true
			announced := c.itemValue(item, "in_progress")
			announced["arguments"] = ""
			c.event(&out, "response.output_item.added", map[string]interface{}{"output_index": item.index, "item": announced})
			if item.arguments != "" {
				c.event(&out, "response.function_call_arguments.delta", map[string]interface{}{"item_id": item.id, "output_index": item.index, "delta": item.arguments})
			}
		} else if item.added && tc.Function.Arguments != "" {
			c.event(&out, "response.function_call_arguments.delta", map[string]interface{}{"item_id": item.id, "output_index": item.index, "delta": tc.Function.Arguments})
		}
	}
	if out.Len() == 0 {
		return nil
	}
	return out.Bytes()
}

func (c *responsesDownStreamConverter) Finish() []byte {
	if c.finished || c.failed || !c.terminal {
		return nil
	}
	c.finished = true
	var out bytes.Buffer
	c.start(&out, "", "")
	for _, item := range c.items {
		if item.kind == "function_call" && !item.added {
			c.fail(&out)
			return out.Bytes()
		}
	}
	status, eventType := "completed", "response.completed"
	reason := ""
	if c.finishReason == "length" {
		status, eventType, reason = "incomplete", "response.incomplete", "max_output_tokens"
	}
	if c.finishReason == "content_filter" {
		status, eventType, reason = "incomplete", "response.incomplete", "content_filter"
	}
	output := make([]interface{}, 0, len(c.items))
	for _, item := range c.items {
		if item.kind == "message" {
			c.event(&out, "response.output_text.done", map[string]interface{}{"item_id": item.id, "output_index": item.index, "content_index": 0, "text": item.text})
			c.event(&out, "response.content_part.done", map[string]interface{}{"item_id": item.id, "output_index": item.index, "content_index": 0, "part": map[string]interface{}{"type": "output_text", "text": item.text, "annotations": []interface{}{}}})
		} else {
			c.event(&out, "response.function_call_arguments.done", map[string]interface{}{"item_id": item.id, "output_index": item.index, "arguments": item.arguments, "name": item.name})
		}
		value := c.itemValue(item, status)
		c.event(&out, "response.output_item.done", map[string]interface{}{"output_index": item.index, "item": value})
		output = append(output, value)
	}
	response := map[string]interface{}{"id": c.responseID, "object": "response", "status": status, "model": c.model, "output": output,
		"usage": map[string]int{"input_tokens": c.inputTokens, "output_tokens": c.outputTokens, "total_tokens": c.inputTokens + c.outputTokens}}
	if reason != "" {
		response["incomplete_details"] = map[string]string{"reason": reason}
	}
	c.event(&out, eventType, map[string]interface{}{"response": response})
	return out.Bytes()
}

func writeResponsesEvent(buf *bytes.Buffer, event string, payload map[string]interface{}) {
	data, _ := json.Marshal(payload)
	fmt.Fprintf(buf, "event: %s\ndata: %s\n\n", event, data)
}
