package adapter

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func protocolData(t *testing.T, output []byte) []map[string]interface{} {
	t.Helper()
	var events []map[string]interface{}
	for _, line := range bytes.Split(output, []byte("\n")) {
		raw := responsesSSEPayload(line)
		if len(raw) == 0 || bytes.Equal(raw, []byte("[DONE]")) {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatalf("invalid event: %s: %v", raw, err)
		}
		events = append(events, event)
	}
	return events
}

func TestNativeResponsesNeverReplayedOrNormalized(t *testing.T) {
	CaptureResponsesReplay([]byte(`{"id":"resp_prior","output":[{"type":"reasoning","id":"rs_1"},{"type":"function_call","call_id":"call_1","name":"read","arguments":"{}"}]}`))
	for _, raw := range []string{
		` { "model":"gpt-5", "previous_response_id":"resp_prior", "input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}], "reasoning_effort":"high" } `,
		`{"model":"gpt-5","input":[{"type":"reasoning","id":"rs_1","encrypted_content":"opaque"}]}`,
		`{"input":"hello","reasoning_effort":"high"}`, `not-json`,
	} {
		if got := NormalizeResponsesRequest([]byte(raw)); !bytes.Equal(got, []byte(raw)) {
			t.Fatalf("native body mutated: %s", got)
		}
	}
}

func TestResponsesSessionHeadersPreservedWithoutCredentials(t *testing.T) {
	src := http.Header{}
	names := []string{"session_id", "SESSION-ID", "conversation_id", "openai-conversation-id", "x-codex-session-id", "x-codex-turn-metadata", "x-codex-window-id", "x-codex-turn-state", "openai-beta", "originator"}
	for _, name := range names {
		src.Add(name, "one")
		src.Add(name, "two")
	}
	src.Set("Authorization", "do-not-copy")
	src.Set("Cookie", "do-not-copy")
	src.Set("Connection", "keep-alive")
	dst := http.Header{}
	CopyResponsesSessionHeaders(src, dst.Add)
	for _, name := range names {
		if len(dst.Values(name)) != 2 {
			t.Errorf("header %s missing or truncated: %v", name, dst.Values(name))
		}
	}
	for _, name := range []string{"Authorization", "Cookie", "Connection"} {
		if dst.Get(name) != "" {
			t.Errorf("unexpected credential/hop header: %s", name)
		}
	}
}

func TestResponsesToolChoiceAndReasoningRoundTrip(t *testing.T) {
	for _, choice := range []string{`"auto"`, `"required"`, `{"type":"function","name":"read"}`} {
		body := []byte(`{"model":"gpt-5","input":"hello","reasoning":{"effort":"high","summary":"auto"},"tool_choice":` + choice + `}`)
		canonical, err := (&ResponsesDownstreamAdapter{}).RequestToCanonical(body)
		if err != nil {
			t.Fatal(err)
		}
		var c map[string]interface{}
		_ = json.Unmarshal(canonical, &c)
		if c["reasoning"].(map[string]interface{})["effort"] != "high" {
			t.Fatalf("reasoning lost: %s", canonical)
		}
		if strings.HasPrefix(choice, "{") {
			if c["tool_choice"].(map[string]interface{})["function"].(map[string]interface{})["name"] != "read" {
				t.Fatalf("not canonical choice: %s", canonical)
			}
		}
		converted, err := (&ResponsesAdapter{}).ConvertRequest("gpt-5", canonical)
		if err != nil {
			t.Fatal(err)
		}
		var actual map[string]json.RawMessage
		_ = json.Unmarshal(converted, &actual)
		var want, got interface{}
		_ = json.Unmarshal([]byte(choice), &want)
		_ = json.Unmarshal(actual["tool_choice"], &got)
		w, _ := json.Marshal(want)
		g, _ := json.Marshal(got)
		if !bytes.Equal(w, g) {
			t.Fatalf("choice changed: %s -> %s", choice, actual["tool_choice"])
		}
		if !bytes.Contains(actual["reasoning"], []byte(`"summary":"auto"`)) {
			t.Fatalf("reasoning lost: %s", converted)
		}
	}
}

func TestResponsesUpstreamParallelToolIdentityAndIndexes(t *testing.T) {
	c := (&ResponsesAdapter{}).NewStreamConverter()
	inputs := []string{
		`{"type":"response.created","response":{"id":"resp_p","model":"gpt-5"}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_A","call_id":"call_A","name":"first","arguments":""}}`,
		`{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc_B","call_id":"call_B","name":"second","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_A","output_index":1,"delta":"{\"a\":"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_B","output_index":2,"delta":"{}"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_A","output_index":1,"delta":"1}"}`,
		`{"type":"response.function_call_arguments.done","item_id":"fc_A","output_index":1,"arguments":"{\"a\":1}"}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":5}}}`,
	}
	var output []byte
	for _, raw := range inputs {
		output = append(output, c.Convert([]byte("data:"+raw))...)
	}
	output = append(output, c.Finish()...)
	ids, names, args := map[int]string{}, map[int]string{}, map[int]string{}
	finish := ""
	for _, e := range protocolData(t, output) {
		choices, _ := e["choices"].([]interface{})
		if len(choices) == 0 {
			continue
		}
		ch := choices[0].(map[string]interface{})
		if reason, ok := ch["finish_reason"].(string); ok {
			finish = reason
		}
		delta := ch["delta"].(map[string]interface{})
		tools, _ := delta["tool_calls"].([]interface{})
		for _, raw := range tools {
			tc := raw.(map[string]interface{})
			i := int(tc["index"].(float64))
			if id, ok := tc["id"].(string); ok {
				ids[i] = id
			}
			fn := tc["function"].(map[string]interface{})
			if n, ok := fn["name"].(string); ok {
				names[i] += n
			}
			if a, ok := fn["arguments"].(string); ok {
				args[i] += a
			}
		}
	}
	if ids[0] != "call_A" || ids[1] != "call_B" || names[0] != "first" || names[1] != "second" || args[0] != `{"a":1}` || args[1] != "{}" || finish != "tool_calls" {
		t.Fatalf("bad tool chain: ids=%v names=%v args=%v finish=%s\n%s", ids, names, args, finish, output)
	}
	if c.Finish() != nil {
		t.Fatal("Finish must be idempotent")
	}
}

func TestResponsesUpstreamFinishRequiresSuccessfulTerminal(t *testing.T) {
	for _, terminal := range []string{"", `{"type":"response.failed","response":{"status":"failed"}}`, `{"type":"error","error":{"message":"failed"}}`} {
		c := (&ResponsesAdapter{}).NewStreamConverter()
		c.Convert([]byte(`data: {"type":"response.output_text.delta","delta":"partial"}`))
		out := c.Convert([]byte("data: " + terminal))
		tail := c.Finish()
		if len(tail) != 0 || bytes.Contains(out, []byte(`"finish_reason":"stop"`)) {
			t.Fatalf("failed/truncated stream became success: %s %s", out, tail)
		}
	}
	c := (&ResponsesAdapter{}).NewStreamConverter()
	c.Convert([]byte(`data: {"type":"response.incomplete","response":{"status":"incomplete"}}`))
	if out := c.Finish(); !bytes.Contains(out, []byte(`"finish_reason":"length"`)) {
		t.Fatalf("incomplete not retained: %s", out)
	}
}

func TestResponsesDownstreamParallelToolsDoneAndFullOutput(t *testing.T) {
	c := (&ResponsesDownstreamAdapter{}).NewStreamConverter()
	input := []string{
		`{"id":"chatcmpl-p","model":"gpt-5","choices":[{"delta":{"content":"before"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_A","function":{"name":"first","arguments":"{\"a\":"}},{"index":1,"id":"call_B","function":{"name":"second","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}],"content":"after"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4}}`,
	}
	var output []byte
	for _, raw := range input {
		output = append(output, c.Convert([]byte("data: "+raw))...)
	}
	output = append(output, c.Finish()...)
	var completed map[string]interface{}
	doneCount := 0
	itemIDs := map[string]string{}
	args := map[string]string{}
	textIDs := map[string]bool{}
	for _, e := range protocolData(t, output) {
		typ, _ := e["type"].(string)
		switch typ {
		case "response.output_item.added":
			item := e["item"].(map[string]interface{})
			if item["type"] == "function_call" {
				itemIDs[item["id"].(string)] = item["call_id"].(string)
			}
		case "response.function_call_arguments.delta":
			args[e["item_id"].(string)] += e["delta"].(string)
		case "response.function_call_arguments.done":
			doneCount++
		case "response.output_text.delta", "response.output_text.done":
			textIDs[e["item_id"].(string)] = true
		case "response.completed":
			completed = e["response"].(map[string]interface{})
		}
	}
	if completed == nil || doneCount != 2 || len(textIDs) != 1 {
		t.Fatalf("missing completion or unstable text id: %s", output)
	}
	full := completed["output"].([]interface{})
	if len(full) != 3 {
		t.Fatalf("missing completed output: %s", output)
	}
	text := full[0].(map[string]interface{})["content"].([]interface{})[0].(map[string]interface{})["text"]
	if text != "beforeafter" {
		t.Fatalf("missing full text: %v", text)
	}
	for id, call := range itemIDs {
		want := "{}"
		if call == "call_A" {
			want = `{"a":1}`
		}
		if args[id] != want {
			t.Fatalf("wrong parallel arguments: %v", args)
		}
	}
	for _, raw := range full[1:] {
		item := raw.(map[string]interface{})
		want := "{}"
		if item["call_id"] == "call_A" {
			want = `{"a":1}`
		}
		if item["arguments"] != want || item["status"] != "completed" {
			t.Fatalf("bad final item: %v", item)
		}
	}
	if completed["usage"].(map[string]interface{})["total_tokens"] != float64(7) {
		t.Fatal("usage after finish_reason lost")
	}
	if c.Finish() != nil {
		t.Fatal("Finish must be idempotent")
	}
}

func TestResponsesDownstreamTerminalAndErrorSemantics(t *testing.T) {
	for _, tc := range []struct{ name, terminal, event string }{
		{"truncated", "", ""}, {"done", "data: [DONE]", "response.completed"},
		{"incomplete", `data: {"choices":[{"delta":{},"finish_reason":"length"}]}`, "response.incomplete"},
		{"filter", `data: {"choices":[{"delta":{},"finish_reason":"content_filter"}]}`, "response.incomplete"},
		{"failed", `data: {"error":{"message":"upstream failed"}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := (&ResponsesDownstreamAdapter{}).NewStreamConverter()
			c.Convert([]byte(`data: {"id":"c","choices":[{"delta":{"content":"partial"}}]}`))
			c.Convert([]byte(tc.terminal))
			out := c.Finish()
			if tc.event == "" {
				if len(out) != 0 {
					t.Fatalf("unexpected success: %s", out)
				}
			} else if !bytes.Contains(out, []byte("event: "+tc.event+"\n")) {
				t.Fatalf("missing %s: %s", tc.event, out)
			}
			if tc.event == "response.incomplete" && bytes.Contains(out, []byte("event: response.completed")) {
				t.Fatalf("incomplete became completed: %s", out)
			}
		})
	}
}
