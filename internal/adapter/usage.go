package adapter

// SelectUsageAdapter 选择用于提取用量的适配器。
//
// 非透传请求的响应已经转换为规范格式，继续使用渠道适配器即可。
// 透传请求不会经过协议转换，必须按真实下游协议解析原始响应；
// 例如 openai 渠道透传 Responses 请求时，渠道适配器仍是 OpenAIAdapter，
// 但实际响应 usage 字段是 input_tokens/output_tokens，应该使用 ResponsesAdapter。
func SelectUsageAdapter(fallback Adapter, passthrough bool, protocol string) Adapter {
	if !passthrough {
		return fallback
	}
	switch protocol {
	case "responses":
		return &ResponsesAdapter{}
	case "anthropic":
		return &AnthropicAdapter{}
	default:
		return &OpenAIAdapter{}
	}
}
