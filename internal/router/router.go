// Package router 提供模型路由规则框架。
//
// 路由规则（Rule）在请求进入普通模型链路前被依次匹配，命中的规则可以：
//   - 转换请求体（如替换模型名、注入提示词、图片转描述等），返回 NewBody 后由调用方继续走普通链路
//   - 完全接管请求（如返回错误），设置 Handled=true 并附带状态码/响应体
//
// 框架与传输层解耦：HTTP 中转（handler/proxy.go）与 MITM 代理（proxy/adapter.go）
// 共用同一套规则实现，只需将结果映射到各自的响应机制。
//
// 新增路由规则的步骤：
//  1. 实现 Rule 接口（Name/Match/Transform）
//  2. 在 RouterRegistry（各调用方构建规则链处）注册
package router

import (
	"encoding/json"
	"fmt"
)

// 下游协议常量
const (
	ProtocolOpenAI     = "openai"
	ProtocolAnthropic  = "anthropic"
	ProtocolResponses  = "responses"
)

// Context 模型路由上下文（与传输层解耦：gin / MITM 通用）
type Context struct {
	// Protocol 下游协议：openai / anthropic / responses
	Protocol string
	// RawBody 原始请求体（下游协议格式）
	RawBody []byte
	// Model 请求的模型名（可能是虚拟模型名）
	Model string
	// APIKeyID 已校验的 API Key ID（备用，当前识图调用不单独计费）
	APIKeyID *int64
	// ClientAuth 下游透传的 Authorization 头（渠道未配置 key 时用于识图请求）
	ClientAuth string
	// Stream 是否流式请求
	Stream bool
	// StreamPreface 流式预响应回调（nil = 不支持预响应）。
	// 规则在耗时操作（如识图）前调用，向客户端先行发送一段流式提示
	// （如"正在分析图片..."），避免下游长时间无响应。回调负责写入并冲刷
	// 符合下游协议的 SSE 帧；返回 error 表示客户端已断开。
	// 仅 HTTP 中转侧 openai 协议提供（MITM 侧为同步响应，无流式通道）。
	StreamPreface func(model, content string) error
}

// Result 路由处理结果
type Result struct {
	// NewBody 转换后的请求体（nil = 不修改请求体）
	NewBody []byte
	// Handled true = 规则已完全接管（使用 StatusCode/RespBody 写响应）
	Handled bool
	// StatusCode Handled=true 时的 HTTP 状态码
	StatusCode int
	// RespBody Handled=true 时的响应体（JSON）
	RespBody []byte
}

// Rule 模型路由规则接口。
// 后续新路由规则（模型别名、提示词注入、降级路由、内容过滤等）
// 只需实现此接口并在规则链中注册。
type Rule interface {
	// Name 规则名称（日志用）
	Name() string
	// Match 判断是否命中此规则
	Match(ctx *Context) bool
	// Transform 转换/处理请求，返回结果。
	// 返回 nil 表示未命中后续无需处理（调用方继续普通链路）。
	Transform(ctx *Context) *Result
}

// ApplyRules 依次应用路由规则，返回第一个命中规则的转换结果。
// 未命中任何规则时返回 nil。
func ApplyRules(rules []Rule, ctx *Context) *Result {
	for _, r := range rules {
		if !r.Match(ctx) {
			continue
		}
		res := r.Transform(ctx)
		if res == nil {
			continue // 规则匹配但放弃处理，尝试下一条
		}
		return res
	}
	return nil
}

// ErrorResult 构造错误响应结果（Handled=true）
func ErrorResult(status int, format string, args ...interface{}) *Result {
	body, _ := json.Marshal(map[string]string{"error": fmt.Sprintf(format, args...)})
	return &Result{
		Handled:    true,
		StatusCode: status,
		RespBody:   body,
	}
}

// JSONResult 构造 JSON 响应结果（Handled=true）
func JSONResult(status int, payload interface{}) *Result {
	body, err := json.Marshal(payload)
	if err != nil {
		return ErrorResult(500, "序列化响应失败: %v", err)
	}
	return &Result{
		Handled:    true,
		StatusCode: status,
		RespBody:   body,
	}
}
