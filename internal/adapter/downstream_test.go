package adapter

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// ===== 透传适配器 =====

func TestProtocolURL(t *testing.T) {
	cases := []struct {
		base     string
		protocol string
		want     string
	}{
		{"https://api.openai.com", "openai", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1", "openai", "https://api.openai.com/v1/chat/completions"},
		{"https://api.anthropic.com", "anthropic", "https://api.anthropic.com/v1/messages"},
		{"https://api.openai.com", "responses", "https://api.openai.com/v1/responses"},
		{"https://api.example.com/", "openai", "https://api.example.com/v1/chat/completions"},
	}
	for _, c := range cases {
		if got := ProtocolURL(c.base, c.protocol); got != c.want {
			t.Errorf("ProtocolURL(%s, %s) = %s, want %s", c.base, c.protocol, got, c.want)
		}
	}
}

func TestPassthroughAdapter_NoConversion(t *testing.T) {
	a := NewPassthroughDownstreamAdapter("anthropic")
	if !a.IsPassthrough() {
		t.Fatal("透传适配器 IsPassthrough 应为 true")
	}
	if a.Protocol() != "anthropic" {
		t.Fatalf("Protocol 应为 anthropic，got %s", a.Protocol())
	}

	reqBody := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}]}`)
	out, err := a.RequestToCanonical(reqBody)
	if err != nil || !bytes.Equal(out, reqBody) {
		t.Fatalf("请求体应原样透传: %v", err)
	}

	respBody := []byte(`{"id":"msg_1","type":"message","content":[]}`)
	out, err = a.ResponseToDownstream(respBody)
	if err != nil || !bytes.Equal(out, respBody) {
		t.Fatalf("响应体应原样透传: %v", err)
	}

	// 流式也应原样转发
	conv := a.NewStreamConverter()
	line := []byte("event: message_start\ndata: {}\n\n")
	if out := conv.Convert(line); !bytes.Equal(out, line) {
		t.Fatalf("流式事件应原样透传: %s", string(out))
	}
	if fin := conv.Finish(); fin != nil {
		t.Fatalf("透传 Finish 应为 nil，got %s", string(fin))
	}
}

func TestPassthroughAdapter_ProtocolMatch(t *testing.T) {
	// 模拟 tryForward 中的协议一致性判断
	cases := []struct {
		downstream  string
		upstream    string
		passthrough bool
	}{
		{"openai", "openai", true},
		{"anthropic", "anthropic", true},
		{"responses", "responses", true},
		{"responses", "openai", true}, // 模型声明支持 responses，协议优先
		{"openai", "responses", true}, // 模型声明支持 openai，协议优先
		{"anthropic", "openai", false},
		{"openai", "anthropic", false},
		{"anthropic", "gemini", false},
		{"openai", "gemini", false},
	}
	for _, c := range cases {
		// 对跨协议 true 的案例，模拟渠道和模型都明确声明支持下游协议。
		channelSupports := c.passthrough && c.downstream != c.upstream
		modelSupports := c.passthrough && c.downstream != c.upstream
		if got := CanPassthrough(c.upstream, c.downstream, channelSupports, modelSupports); got != c.passthrough {
			t.Errorf("downstream=%s upstream=%s: 期望 passthrough=%v，got %v",
				c.downstream, c.upstream, c.passthrough, got)
		}
	}
}

// ===== Anthropic 请求 → 规范格式 =====

func TestAnthropicRequestToCanonical_Basic(t *testing.T) {
	a := &AnthropicDownstreamAdapter{}
	body := []byte(`{
		"model": "claude-sonnet-4",
		"max_tokens": 4096,
		"system": "You are helpful",
		"messages": [
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi there"}
		],
		"stream": true
	}`)

	out, err := a.RequestToCanonical(body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var req openAICanonicalRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("结果不是合法 JSON: %v", err)
	}
	if req.Model != "claude-sonnet-4" {
		t.Errorf("Model 应为 claude-sonnet-4，got %s", req.Model)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("Messages 应为 3 条（system+user+assistant），got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "You are helpful" {
		t.Errorf("system 消息转换错误: %+v", req.Messages[0])
	}
	if req.Messages[1].Role != "user" || req.Messages[1].Content != "Hello" {
		t.Errorf("user 消息转换错误: %+v", req.Messages[1])
	}
	if req.Messages[2].Role != "assistant" || req.Messages[2].Content != "Hi there" {
		t.Errorf("assistant 消息转换错误: %+v", req.Messages[2])
	}
	if !req.Stream {
		t.Error("Stream 应为 true")
	}
}

func TestAnthropicRequestToCanonical_Tools(t *testing.T) {
	a := &AnthropicDownstreamAdapter{}
	body := []byte(`{
		"model": "claude-sonnet-4",
		"max_tokens": 1024,
		"tools": [{
			"name": "get_weather",
			"description": "Get weather",
			"input_schema": {"type": "object", "properties": {"city": {"type": "string"}}}
		}],
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "weather?"}
			]},
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": {"city": "SF"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_1", "content": "70F"}
			]}
		]
	}`)

	out, err := a.RequestToCanonical(body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var req openAICanonicalRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("结果不是合法 JSON: %v", err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("Tools 应为 1 个，got %d", len(req.Tools))
	}
	if req.Tools[0].Function.Name != "get_weather" {
		t.Errorf("Tool 名称错误: %s", req.Tools[0].Function.Name)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("Messages 应为 3 条，got %d", len(req.Messages))
	}

	// assistant 消息应带 tool_calls
	assistant := req.Messages[1]
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant 消息应有 1 个 tool_call，got %d", len(assistant.ToolCalls))
	}
	tc := assistant.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Function.Name != "get_weather" {
		t.Errorf("tool_call 转换错误: %+v", tc)
	}

	// tool_result 消息应转为 tool 角色
	toolMsg := req.Messages[2]
	if toolMsg.Role != "tool" || toolMsg.ToolCallID != "toolu_1" || toolMsg.Content != "70F" {
		t.Errorf("tool_result 转换错误: %+v", toolMsg)
	}
}

// ===== 规范格式响应 → Anthropic =====

func TestAnthropicResponseToDownstream(t *testing.T) {
	a := &AnthropicDownstreamAdapter{}
	body := []byte(`{
		"id": "chatcmpl-abc123",
		"object": "chat.completion",
		"created": 0,
		"model": "claude-sonnet-4",
		"choices": [{
			"index": 0,
			"message": {"role": "assistant", "content": "Hello!"},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`)

	out, err := a.ResponseToDownstream(body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var resp anthropicDownResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("结果不是合法 JSON: %v", err)
	}
	if resp.Type != "message" || resp.Role != "assistant" {
		t.Errorf("Type/Role 错误: %s/%s", resp.Type, resp.Role)
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("StopReason 应为 end_turn，got %s", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "Hello!" {
		t.Errorf("Content 转换错误: %+v", resp.Content)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Errorf("Usage 转换错误: %+v", resp.Usage)
	}
}

// ===== 流式转换 =====

func TestAnthropicStreamConverter(t *testing.T) {
	a := &AnthropicDownstreamAdapter{}
	conv := a.NewStreamConverter()

	// 模拟 OpenAI SSE 流
	events := []string{
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"claude-sonnet-4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"claude-sonnet-4","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"claude-sonnet-4","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","model":"claude-sonnet-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
		`data: [DONE]`,
	}

	var out strings.Builder
	for _, e := range events {
		if got := conv.Convert([]byte(e + "\n")); got != nil {
			out.Write(got)
		}
	}
	out.Write(conv.Finish())

	output := out.String()
	// 必须包含 message_start
	if !strings.Contains(output, "event: message_start") {
		t.Errorf("缺少 message_start 事件:\n%s", output)
	}
	// 必须包含 content_block_start
	if !strings.Contains(output, "event: content_block_start") {
		t.Errorf("缺少 content_block_start 事件:\n%s", output)
	}
	// 必须包含 text_delta
	if !strings.Contains(output, "text_delta") || !strings.Contains(output, "Hello") || !strings.Contains(output, " world") {
		t.Errorf("缺少 text_delta 内容:\n%s", output)
	}
	// 必须包含 message_delta + stop_reason
	if !strings.Contains(output, "event: message_delta") || !strings.Contains(output, "end_turn") {
		t.Errorf("缺少 message_delta/end_turn:\n%s", output)
	}
	// 必须包含 message_stop
	if !strings.Contains(output, "event: message_stop") {
		t.Errorf("缺少 message_stop 事件:\n%s", output)
	}
	// usage：OpenAI 流式在结尾 chunk 才返回 usage，此时 message_start 已发出
	// 因此 message_start 中 input_tokens 为 0，output_tokens 出现在 message_delta
	if !strings.Contains(output, `"input_tokens":0`) {
		t.Errorf("message_start 应包含 input_tokens:0:\n%s", output)
	}
	if !strings.Contains(output, `"output_tokens":5`) {
		t.Errorf("缺少 output_tokens usage:\n%s", output)
	}
}

func TestAnthropicStreamConverter_ToolUse(t *testing.T) {
	a := &AnthropicDownstreamAdapter{}
	conv := a.NewStreamConverter()

	events := []string{
		`data: {"id":"c1","model":"claude","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"c1","model":"claude","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"toolu_1","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"c1","model":"claude","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"SF\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"c1","model":"claude","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}

	var out strings.Builder
	for _, e := range events {
		if got := conv.Convert([]byte(e + "\n")); got != nil {
			out.Write(got)
		}
	}
	out.Write(conv.Finish())

	output := out.String()
	if !strings.Contains(output, `"type":"tool_use"`) {
		t.Errorf("缺少 tool_use content_block_start:\n%s", output)
	}
	if !strings.Contains(output, "input_json_delta") || !strings.Contains(output, `\"city\":\"SF\"`) {
		t.Errorf("缺少 input_json_delta:\n%s", output)
	}
	if !strings.Contains(output, "tool_use") {
		t.Errorf("stop_reason 应为 tool_use:\n%s", output)
	}
}
