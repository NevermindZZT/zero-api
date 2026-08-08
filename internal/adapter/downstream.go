package adapter

// DownstreamAdapter 下游协议适配器
// 定义客户端（下游）如何与 zero-api 通信。
// zero-api 内部统一使用 OpenAI Chat Completions 格式作为规范格式（canonical format），
// 下游协议在入口处转换为规范格式，响应时再转回下游协议。
// 当下游协议与上游渠道协议一致时，使用 PassthroughDownstreamAdapter 原样透传，不做任何转换。
type DownstreamAdapter interface {
	// Protocol 下游协议名称（如 openai、anthropic）
	Protocol() string

	// IsPassthrough 是否为透传模式（下游协议与上游渠道协议一致，无需转换）
	IsPassthrough() bool

	// RequestToCanonical 将下游协议请求转换为 OpenAI 规范格式请求
	RequestToCanonical(body []byte) ([]byte, error)

	// ResponseToDownstream 将 OpenAI 规范格式响应转换为下游协议响应（非流式）
	ResponseToDownstream(body []byte) ([]byte, error)

	// NewStreamConverter 创建流式 SSE 转换器
	NewStreamConverter() StreamConverter
}

// StreamConverter 流式 SSE 事件转换器
// 将上游（规范格式）的 SSE 事件流转换为下游协议的 SSE 事件流
type StreamConverter interface {
	// Convert 处理一行上游 SSE 数据（含换行符，可能为 data: 开头或注释行）
	// 返回需要写回客户端的字节（含换行符）；返回 nil 表示该行无需转发
	Convert(line []byte) []byte

	// Finish 流结束时调用，返回需要补发的收尾事件；返回 nil 表示无需补发
	Finish() []byte
}

// OpenAIDownstreamAdapter OpenAI 规范格式下游适配器（透传）
type OpenAIDownstreamAdapter struct{}

func (a *OpenAIDownstreamAdapter) Protocol() string   { return "openai" }
func (a *OpenAIDownstreamAdapter) IsPassthrough() bool { return false }

func (a *OpenAIDownstreamAdapter) RequestToCanonical(body []byte) ([]byte, error) {
	return body, nil
}

func (a *OpenAIDownstreamAdapter) ResponseToDownstream(body []byte) ([]byte, error) {
	return body, nil
}

func (a *OpenAIDownstreamAdapter) NewStreamConverter() StreamConverter {
	return &openaiStreamConverter{}
}

type openaiStreamConverter struct{}

func (c *openaiStreamConverter) Convert(line []byte) []byte {
	return line
}

func (c *openaiStreamConverter) Finish() []byte { return nil }

// NewDownstreamAdapter 根据下游协议名称创建适配器
func NewDownstreamAdapter(protocol string) DownstreamAdapter {
	switch protocol {
	case "anthropic":
		return &AnthropicDownstreamAdapter{}
	case "responses":
		return &ResponsesDownstreamAdapter{}
	default:
		return &OpenAIDownstreamAdapter{}
	}
}

// passthroughDownstreamAdapter 透传适配器：下游协议与上游渠道协议一致时使用
// 所有转换均为恒等函数，请求/响应/流式事件原样转发，不做任何转换
// （避免规范格式往返转换丢失协议原生特性，如 Anthropic 的 tools/thinking 参数）
type passthroughDownstreamAdapter struct {
	protocol string // 原始下游协议名（保留信息用于日志/调试）
}

func (a *passthroughDownstreamAdapter) Protocol() string   { return a.protocol }
func (a *passthroughDownstreamAdapter) IsPassthrough() bool { return true }

func (a *passthroughDownstreamAdapter) RequestToCanonical(body []byte) ([]byte, error) {
	return body, nil
}

func (a *passthroughDownstreamAdapter) ResponseToDownstream(body []byte) ([]byte, error) {
	return body, nil
}

func (a *passthroughDownstreamAdapter) NewStreamConverter() StreamConverter {
	return &passthroughStreamConverter{}
}

// NewPassthroughDownstreamAdapter 创建透传适配器
func NewPassthroughDownstreamAdapter(protocol string) DownstreamAdapter {
	return &passthroughDownstreamAdapter{protocol: protocol}
}

// passthroughStreamConverter 透流传式转换器：SSE 事件原样转发
// 注意：与原样逐行透传不同，此转换器原样转发所有行（含注释、event 行），
// 与 openaiStreamConverter 行为一致，但语义上明确表示“无转换”。
type passthroughStreamConverter struct{}

func (c *passthroughStreamConverter) Convert(line []byte) []byte {
	return line
}

func (c *passthroughStreamConverter) Finish() []byte { return nil }
