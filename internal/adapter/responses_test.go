package adapter

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// ===== Responses 上游适配器 =====

func TestResponsesAdapter_ConvertRequest(t *testing.T) {
	a := &ResponsesAdapter{}
	// OpenAI 规范格式请求 → Responses 格式
	body := []byte(`{
		"model": "gpt-5",
		"messages": [
			{"role": "system", "content": "You are helpful"},
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "", "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"SF\"}"}}]},
			{"role": "tool", "content": "70F", "tool_call_id": "call_1"}
		],
		"max_tokens": 1024,
		"stream": true
	}`)

	out, err := a.ConvertRequest("gpt-5", body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("结果不是合法 JSON: %v", err)
	}
	if req["model"] != "gpt-5" {
		t.Errorf("model 错误: %v", req["model"])
	}
	if req["instructions"] != "You are helpful" {
		t.Errorf("instructions 错误: %v", req["instructions"])
	}
	if req["max_output_tokens"] != float64(1024) {
		t.Errorf("max_output_tokens 错误: %v", req["max_output_tokens"])
	}
	if req["stream"] != true {
		t.Error("stream 应为 true")
	}
	input, ok := req["input"].([]interface{})
	if !ok {
		t.Fatalf("input 不是数组: %v", req["input"])
	}
	// 3 个输入项：user 消息 + function_call + function_call_output
	if len(input) != 3 {
		t.Fatalf("input 应有 3 项，got %d: %v", len(input), input)
	}
	// 检查 function_call 转换
	fc := input[1].(map[string]interface{})
	if fc["type"] != "function_call" || fc["name"] != "get_weather" {
		t.Errorf("function_call 转换错误: %v", fc)
	}
	// 检查 function_call_output 转换
	fco := input[2].(map[string]interface{})
	if fco["type"] != "function_call_output" || fco["output"] != "70F" {
		t.Errorf("function_call_output 转换错误: %v", fco)
	}
}

func TestResponsesAdapter_ConvertResponse(t *testing.T) {
	a := &ResponsesAdapter{}
	// Responses 格式响应 → OpenAI 规范格式
	body := []byte(`{
		"id": "resp_123",
		"object": "response",
		"model": "gpt-5",
		"status": "completed",
		"output": [
			{"type": "message", "id": "msg_1", "role": "assistant", "content": [{"type": "output_text", "text": "Hello!"}]},
			{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "get_weather", "arguments": "{\"city\":\"SF\"}"}
		],
		"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
	}`)

	out, err := a.ConvertResponse(body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var resp OpenAIResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("结果不是合法 JSON: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices 应为 1，got %d", len(resp.Choices))
	}
	msg := resp.Choices[0].Message
	if msg.Content != "Hello!" {
		t.Errorf("content 错误: %q", msg.Content)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("tool_calls 转换错误: %+v", msg.ToolCalls)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason 应为 tool_calls，got %s", resp.Choices[0].FinishReason)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 {
		t.Errorf("usage 错误: %+v", resp.Usage)
	}
}

func TestResponsesAdapterExtractUsagePrefersInputOutputOverTotalOnly(t *testing.T) {
	a := &ResponsesAdapter{}
	body := []byte(`{"id":"resp_1","object":"response","usage":{"input_tokens":607790,"output_tokens":412,"total_tokens":608202}}`)
	usage, err := a.ExtractUsage(body)
	if err != nil {
		t.Fatal(err)
	}
	if usage.PromptTokens != 607790 || usage.CompletionTokens != 412 || usage.TotalTokens != 608202 {
		t.Fatalf("usage parsed incorrectly: %+v", usage)
	}
}

// ===== Responses 上游流转换器 =====

func TestResponsesUpstreamStreamConverter(t *testing.T) {
	a := &ResponsesAdapter{}
	conv := a.NewStreamConverter()

	events := []string{
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`,
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hello"}`,
		`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":" world"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":5}}}`,
	}

	var out strings.Builder
	for _, e := range events {
		if got := conv.Convert([]byte(e + "\n")); got != nil {
			out.Write(got)
		}
	}
	out.Write(conv.Finish())

	output := out.String()
	if !strings.Contains(output, "chat.completion.chunk") {
		t.Errorf("缺少 OpenAI chunk:\n%s", output)
	}
	if !strings.Contains(output, `"content":"Hello"`) || !strings.Contains(output, `"content":" world"`) {
		t.Errorf("缺少文本内容:\n%s", output)
	}
	if !strings.Contains(output, "[DONE]") {
		t.Errorf("缺少 [DONE]:\n%s", output)
	}
	if !strings.Contains(output, `"prompt_tokens":10`) || !strings.Contains(output, `"completion_tokens":5`) {
		t.Errorf("缺少 usage:\n%s", output)
	}
}

// ===== Responses 下游适配器 =====

func TestResponsesDownstream_RequestToCanonical(t *testing.T) {
	a := &ResponsesDownstreamAdapter{}
	body := []byte(`{
		"model": "gpt-5",
		"instructions": "Be brief",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Hi"}]},
			{"type": "function_call", "call_id": "c1", "name": "get_weather", "arguments": "{}"},
			{"type": "function_call_output", "call_id": "c1", "output": "sunny"}
		],
		"stream": true,
		"max_output_tokens": 500
	}`)

	out, err := a.RequestToCanonical(body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var req openAICanonicalRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("结果不是合法 JSON: %v", err)
	}
	if req.Model != "gpt-5" || req.MaxTokens != 500 || !req.Stream {
		t.Errorf("基础字段错误: %+v", req)
	}
	// system + user + assistant(tool_calls) + tool
	if len(req.Messages) != 4 {
		t.Fatalf("messages 应为 4 条，got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "Be brief" {
		t.Errorf("system 消息错误: %+v", req.Messages[0])
	}
	if req.Messages[1].Role != "user" || req.Messages[1].Content != "Hi" {
		t.Errorf("user 消息错误: %+v", req.Messages[1])
	}
	if req.Messages[2].Role != "assistant" || len(req.Messages[2].ToolCalls) != 1 {
		t.Errorf("assistant tool_call 消息错误: %+v", req.Messages[2])
	}
	if req.Messages[3].Role != "tool" || req.Messages[3].ToolCallID != "c1" || req.Messages[3].Content != "sunny" {
		t.Errorf("tool 消息错误: %+v", req.Messages[3])
	}
}

func TestResponsesDownstream_ResponseToDownstream(t *testing.T) {
	a := &ResponsesDownstreamAdapter{}
	body := []byte(`{
		"id": "chatcmpl-abc",
		"object": "chat.completion",
		"model": "gpt-5",
		"choices": [{
			"index": 0,
			"message": {"role": "assistant", "content": "Hi"},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`)

	out, err := a.ResponseToDownstream(body)
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("结果不是合法 JSON: %v", err)
	}
	if resp["object"] != "response" {
		t.Errorf("object 应为 response，got %v", resp["object"])
	}
	if resp["status"] != "completed" {
		t.Errorf("status 错误: %v", resp["status"])
	}
	output := resp["output"].([]interface{})
	if len(output) != 1 {
		t.Fatalf("output 应有 1 项，got %d", len(output))
	}
	msg := output[0].(map[string]interface{})
	if msg["type"] != "message" {
		t.Errorf("output 类型错误: %v", msg["type"])
	}
}

// ===== 上游流转换器（Anthropic / Gemini） =====

func TestAnthropicUpstreamStreamConverter(t *testing.T) {
	a := &AnthropicAdapter{}
	conv := a.NewStreamConverter()

	events := []string{
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-3","usage":{"input_tokens":10,"output_tokens":0}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`data: {"type":"message_stop"}`,
	}

	var out strings.Builder
	for _, e := range events {
		if got := conv.Convert([]byte(e + "\n")); got != nil {
			out.Write(got)
		}
	}
	out.Write(conv.Finish())

	output := out.String()
	if !strings.Contains(output, "chat.completion.chunk") {
		t.Errorf("缺少 OpenAI chunk:\n%s", output)
	}
	if !strings.Contains(output, `"content":"Hello"`) {
		t.Errorf("缺少文本内容:\n%s", output)
	}
	if !strings.Contains(output, "[DONE]") {
		t.Errorf("缺少 [DONE]:\n%s", output)
	}
	if !strings.Contains(output, `"prompt_tokens":10`) || !strings.Contains(output, `"completion_tokens":5`) {
		t.Errorf("缺少 usage:\n%s", output)
	}
}

func TestGeminiUpstreamStreamConverter(t *testing.T) {
	a := &GeminiAdapter{}
	conv := a.NewStreamConverter()

	events := []string{
		`data: {"candidates":[{"content":{"parts":[{"text":"Hi"}]},"finishReason":""}],"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"totalTokenCount":0}}`,
		`data: {"candidates":[{"content":{"parts":[{"text":" there"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`,
	}

	var out strings.Builder
	for _, e := range events {
		if got := conv.Convert([]byte(e + "\n")); got != nil {
			out.Write(got)
		}
	}
	out.Write(conv.Finish())

	output := out.String()
	if !strings.Contains(output, "chat.completion.chunk") {
		t.Errorf("缺少 OpenAI chunk:\n%s", output)
	}
	if !strings.Contains(output, `"content":"Hi"`) || !strings.Contains(output, `"content":" there"`) {
		t.Errorf("缺少文本内容:\n%s", output)
	}
	if !strings.Contains(output, "[DONE]") {
		t.Errorf("缺少 [DONE]:\n%s", output)
	}
	// Gemini 的 finishReason STOP → stop
	if !strings.Contains(output, `"finish_reason":"stop"`) {
		t.Errorf("finish_reason 应为 stop:\n%s", output)
	}
	if !strings.Contains(output, `"prompt_tokens":10`) || !strings.Contains(output, `"completion_tokens":5`) {
		t.Errorf("缺少 usage:\n%s", output)
	}
}

// 验证 Responses 下游流转换器输出合法事件序列
func TestResponsesDownstreamStreamConverter(t *testing.T) {
	a := &ResponsesDownstreamAdapter{}
	conv := a.NewStreamConverter()

	events := []string{
		`data: {"id":"c1","model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"c1","model":"gpt-5","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: {"id":"c1","model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}

	var out bytes.Buffer
	for _, e := range events {
		if got := conv.Convert([]byte(e + "\n")); got != nil {
			out.Write(got)
		}
	}
	out.Write(conv.Finish())

	output := out.String()
	if !strings.Contains(output, "event: response.created") {
		t.Errorf("缺少 response.created:\n%s", output)
	}
	if !strings.Contains(output, "event: response.output_text.delta") || !strings.Contains(output, `"delta":"Hello"`) {
		t.Errorf("缺少 output_text.delta:\n%s", output)
	}
	if !strings.Contains(output, "event: response.completed") {
		t.Errorf("缺少 response.completed:\n%s", output)
	}
}

func TestResponsesDownstreamPreservesPreviousResponseID(t *testing.T) {
	a := &ResponsesDownstreamAdapter{}
	body := []byte(`{"model":"gpt-5","previous_response_id":"resp_prev","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`)
	canonical, err := a.RequestToCanonical(body)
	if err != nil {
		t.Fatal(err)
	}
	var req openAICanonicalRequest
	if err := json.Unmarshal(canonical, &req); err != nil {
		t.Fatal(err)
	}
	if req.PreviousResponseID != "resp_prev" {
		t.Fatalf("previous_response_id lost: %q", req.PreviousResponseID)
	}
}

func TestResponsesDownstreamStreamUsagePreserved(t *testing.T) {
	conv := (&ResponsesDownstreamAdapter{}).NewStreamConverter()
	if got := conv.Convert([]byte(`data: {"id":"c1","model":"gpt-5","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}],"usage":{"prompt_tokens":123,"completion_tokens":45,"total_tokens":168}}` + "\n")); got == nil {
		t.Fatal("expected converted stream chunk")
	}
	finish := string(conv.Finish())
	if !strings.Contains(finish, `"input_tokens":123`) || !strings.Contains(finish, `"output_tokens":45`) || !strings.Contains(finish, `"total_tokens":168`) {
		t.Fatalf("stream usage was not preserved: %s", finish)
	}
}
