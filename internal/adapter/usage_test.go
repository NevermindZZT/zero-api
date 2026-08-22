package adapter

import "testing"

func TestSelectUsageAdapterUsesPassthroughProtocol(t *testing.T) {
	fallback := &OpenAIAdapter{}
	selected := SelectUsageAdapter(fallback, true, "responses")

	body := []byte(`{"id":"resp_1","usage":{"input_tokens":123,"output_tokens":7,"total_tokens":130}}`)
	usage, err := selected.ExtractUsage(body)
	if err != nil {
		t.Fatalf("Responses 用量应可提取: %v", err)
	}
	if usage.PromptTokens != 123 || usage.CompletionTokens != 7 || usage.TotalTokens != 130 {
		t.Fatalf("用量解析错误: %+v", usage)
	}
}

func TestSelectUsageAdapterKeepsFallbackForConvertedResponse(t *testing.T) {
	fallback := &OpenAIAdapter{}
	selected := SelectUsageAdapter(fallback, false, "responses")
	if selected != fallback {
		t.Fatal("非透传场景应继续使用渠道适配器")
	}
}
