package adapter

import (
	"encoding/json"
	"sync"
	"time"
)

const responsesReplayTTL = 30 * time.Minute
const responsesReplayMaxEntries = 1024

type responsesReplayEntry struct {
	Items     []json.RawMessage
	ExpiresAt time.Time
}

var responsesReplayCache = struct {
	sync.Mutex
	entries map[string]responsesReplayEntry
}{entries: make(map[string]responsesReplayEntry)}

// NormalizeResponsesRequest 补齐 Codex Responses 所需字段，并根据 previous_response_id
// 回放上一轮 reasoning/function_call items，避免孤立 function_call_output。
func NormalizeResponsesRequest(body []byte) []byte {
	var root map[string]interface{}
	if json.Unmarshal(body, &root) != nil {
		return body
	}
	changed := false

	// Responses 使用 reasoning.effort；兼容客户端发送的顶层 reasoning_effort。
	if effort, ok := root["reasoning_effort"].(string); ok && effort != "" {
		if _, exists := root["reasoning"]; !exists {
			root["reasoning"] = map[string]interface{}{"effort": effort}
			changed = true
		}
	}

	input, ok := root["input"].([]interface{})
	if !ok {
		return body
	}
	callIDs := make(map[string]bool)
	outputIDs := make(map[string]bool)
	for _, raw := range input {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := item["type"].(string)
		if typ == "reasoning" {
			if _, exists := item["summary"]; !exists {
				item["summary"] = []interface{}{}
				changed = true
			}
		}
		if callID, _ := item["call_id"].(string); callID != "" {
			if typ == "function_call" || typ == "custom_tool_call" {
				callIDs[callID] = true
			}
			if typ == "function_call_output" || typ == "custom_tool_call_output" {
				outputIDs[callID] = true
			}
		}
	}

	needsReplay := false
	for callID := range outputIDs {
		if !callIDs[callID] {
			needsReplay = true
			break
		}
	}
	previousID, _ := root["previous_response_id"].(string)
	if needsReplay && previousID != "" {
		if replay := getResponsesReplay(previousID); len(replay) > 0 {
			prefix := make([]interface{}, 0, len(replay)+len(input))
			for _, raw := range replay {
				var item interface{}
				if json.Unmarshal(raw, &item) == nil {
					prefix = append(prefix, item)
				}
			}
			root["input"] = append(prefix, input...)
			changed = true
		}
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(root)
	if err != nil {
		return body
	}
	return out
}

// CaptureResponsesReplay 从非流式 JSON 或 SSE response.completed 中缓存可回放条目。
func CaptureResponsesReplay(body []byte) {
	captureResponsesJSON(body)
	for _, line := range splitSSEData(body) {
		captureResponsesJSON(line)
	}
}

func captureResponsesJSON(body []byte) {
	var root struct {
		ID       string            `json:"id"`
		Output   []json.RawMessage `json:"output"`
		Response *struct {
			ID     string            `json:"id"`
			Output []json.RawMessage `json:"output"`
		} `json:"response"`
	}
	if json.Unmarshal(body, &root) != nil {
		return
	}
	id, output := root.ID, root.Output
	if root.Response != nil {
		id, output = root.Response.ID, root.Response.Output
	}
	if id == "" || len(output) == 0 {
		return
	}
	items := make([]json.RawMessage, 0, len(output))
	for _, raw := range output {
		var item struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		if item.Type == "reasoning" || item.Type == "function_call" || item.Type == "custom_tool_call" {
			items = append(items, append(json.RawMessage(nil), raw...))
		}
	}
	if len(items) == 0 {
		return
	}
	responsesReplayCache.Lock()
	defer responsesReplayCache.Unlock()
	cleanupResponsesReplayLocked()
	responsesReplayCache.entries[id] = responsesReplayEntry{Items: items, ExpiresAt: time.Now().Add(responsesReplayTTL)}
}

func getResponsesReplay(id string) []json.RawMessage {
	responsesReplayCache.Lock()
	defer responsesReplayCache.Unlock()
	entry, ok := responsesReplayCache.entries[id]
	if !ok || time.Now().After(entry.ExpiresAt) {
		delete(responsesReplayCache.entries, id)
		return nil
	}
	return entry.Items
}

func cleanupResponsesReplayLocked() {
	now := time.Now()
	for id, entry := range responsesReplayCache.entries {
		if now.After(entry.ExpiresAt) {
			delete(responsesReplayCache.entries, id)
		}
	}
	if len(responsesReplayCache.entries) < responsesReplayMaxEntries {
		return
	}
	for id := range responsesReplayCache.entries {
		delete(responsesReplayCache.entries, id)
		if len(responsesReplayCache.entries) < responsesReplayMaxEntries/2 {
			break
		}
	}
}

func splitSSEData(body []byte) [][]byte {
	var data [][]byte
	start := 0
	for start < len(body) {
		end := start
		for end < len(body) && body[end] != '\n' {
			end++
		}
		line := body[start:end]
		const prefix = "data: "
		if len(line) > len(prefix) && string(line[:len(prefix)]) == prefix {
			data = append(data, append([]byte(nil), line[len(prefix):]...))
		}
		start = end + 1
	}
	return data
}
