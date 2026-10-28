package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type responsesStreamTool struct {
	index                           int
	itemID, callID, name, arguments string
}

type responsesUpstreamStreamConverter struct {
	started, finished, terminal, failed bool
	model, responseID, finishReason     string
	promptTokens, outputTokens          int
	toolsByID                           map[string]*responsesStreamTool
	toolsByOutput                       map[int]*responsesStreamTool
	toolCount                           int
}

func responsesSSEPayload(line []byte) []byte {
	line = bytes.TrimSpace(line)
	if !bytes.HasPrefix(line, []byte("data:")) {
		return nil
	}
	return bytes.TrimSpace(line[len("data:"):])
}

func (c *responsesUpstreamStreamConverter) fail(message string) []byte {
	c.failed = true
	data, _ := json.Marshal(map[string]interface{}{"error": map[string]string{"type": "upstream_error", "message": message}})
	return append(append([]byte("data: "), data...), []byte("\n\n")...)
}

func (c *responsesUpstreamStreamConverter) addTool(out *bytes.Buffer, raw json.RawMessage, outputIndex int) *responsesStreamTool {
	var item struct {
		Type, ID, Name, Arguments string
		CallID                    string `json:"call_id"`
	}
	if json.Unmarshal(raw, &item) != nil || item.Type != "function_call" {
		return nil
	}
	if c.toolsByID == nil {
		c.toolsByID = make(map[string]*responsesStreamTool)
		c.toolsByOutput = make(map[int]*responsesStreamTool)
	}
	tool := c.toolsByID[item.ID]
	if tool == nil && item.ID == "" {
		tool = c.toolsByOutput[outputIndex]
	}
	if tool == nil {
		if item.ID == "" || item.CallID == "" || item.Name == "" {
			c.failed = true
			return nil
		}
		tool = &responsesStreamTool{index: c.toolCount, itemID: item.ID, callID: item.CallID, name: item.Name}
		c.toolCount++
		c.toolsByID[item.ID] = tool
		c.toolsByOutput[outputIndex] = tool
		if !c.started {
			c.emitRoleChunk(out)
		}
		c.emitToolCallStartChunk(out, tool.index, tool.callID, tool.name)
	}
	if item.Arguments != "" && strings.HasPrefix(item.Arguments, tool.arguments) {
		suffix := strings.TrimPrefix(item.Arguments, tool.arguments)
		if suffix != "" {
			c.emitToolCallArgsChunk(out, tool.index, suffix)
			tool.arguments = item.Arguments
		}
	}
	return tool
}

func (c *responsesUpstreamStreamConverter) Convert(line []byte) []byte {
	if c.finished || c.failed || c.terminal {
		return nil
	}
	payload := responsesSSEPayload(line)
	if len(payload) == 0 {
		return nil
	}
	var evt struct {
		Type, Delta           string
		ItemID                string `json:"item_id"`
		OutputIndex           int    `json:"output_index"`
		Item, Response, Error json.RawMessage
		Arguments             string `json:"arguments"`
	}
	if json.Unmarshal(payload, &evt) != nil {
		return nil
	}
	var out bytes.Buffer
	switch evt.Type {
	case "response.created":
		var r struct{ ID, Model string }
		_ = json.Unmarshal(evt.Response, &r)
		c.responseID, c.model = r.ID, r.Model
	case "response.output_item.added", "response.output_item.done":
		c.addTool(&out, evt.Item, evt.OutputIndex)
		if c.failed {
			return c.fail("Responses tool call is missing its identity")
		}
	case "response.output_text.delta":
		if !c.started {
			c.emitRoleChunk(&out)
		}
		if evt.Delta != "" {
			c.emitContentChunk(&out, evt.Delta)
		}
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		tool := c.toolsByID[evt.ItemID]
		if tool == nil && evt.ItemID == "" {
			tool = c.toolsByOutput[evt.OutputIndex]
		}
		if tool == nil {
			return c.fail("Responses tool arguments received without tool identity")
		}
		args := evt.Delta
		if evt.Type == "response.function_call_arguments.done" {
			if !strings.HasPrefix(evt.Arguments, tool.arguments) {
				return c.fail("Responses tool arguments do not match prior deltas")
			}
			args = strings.TrimPrefix(evt.Arguments, tool.arguments)
		}
		if args != "" {
			c.emitToolCallArgsChunk(&out, tool.index, args)
			tool.arguments += args
		}
	case "response.completed", "response.incomplete":
		var r struct {
			ID, Model, Status string
			Output            []json.RawMessage
			Usage             struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			}
		}
		_ = json.Unmarshal(evt.Response, &r)
		if r.Status == "failed" || r.Status == "cancelled" {
			return c.fail("Responses upstream failed")
		}
		if r.ID != "" {
			c.responseID = r.ID
		}
		if r.Model != "" {
			c.model = r.Model
		}
		for i, item := range r.Output {
			c.addTool(&out, item, i)
			if c.failed {
				return c.fail("Responses completed tool call is missing its identity")
			}
		}
		c.terminal = true
		c.finishReason = "stop"
		if c.toolCount > 0 {
			c.finishReason = "tool_calls"
		}
		if evt.Type == "response.incomplete" || r.Status == "incomplete" {
			c.finishReason = "length"
		}
		c.promptTokens, c.outputTokens = r.Usage.InputTokens, r.Usage.OutputTokens
	case "response.failed", "error", "response.error":
		return c.fail("Responses upstream reported " + evt.Type)
	}
	if out.Len() == 0 {
		return nil
	}
	return out.Bytes()
}

func (c *responsesUpstreamStreamConverter) Finish() []byte {
	if c.finished || !c.terminal || c.failed {
		return nil
	}
	c.finished = true
	var out bytes.Buffer
	if !c.started {
		c.emitRoleChunk(&out)
	}
	finish := map[string]interface{}{
		"id": "chatcmpl-" + c.responseID, "object": "chat.completion.chunk", "model": c.model,
		"choices": []interface{}{map[string]interface{}{"index": 0, "delta": map[string]interface{}{}, "finish_reason": c.finishReason}},
		"usage":   map[string]int{"prompt_tokens": c.promptTokens, "completion_tokens": c.outputTokens, "total_tokens": c.promptTokens + c.outputTokens},
	}
	data, _ := json.Marshal(finish)
	fmt.Fprintf(&out, "data: %s\n\ndata: [DONE]\n\n", data)
	return out.Bytes()
}
