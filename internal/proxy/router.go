package proxy

import (
	"encoding/json"
	"strings"
)

// LLM 推理请求的路径特征
var llmInferencePaths = []string{
	"/chat/completions",
	"/v1/chat/completions",
	"/completions",
	"/v1/completions",
	"/embeddings",
	"/v1/embeddings",
	"/v1/chat",
	"/chat",
	"/v1/messages",
}

// LLM 请求体特征字段
var llmFields = []string{
	"messages", "prompt", "max_tokens", "stream",
	"temperature", "top_p", "frequency_penalty",
	"presence_penalty", "n", "stop",
}

// RequestRouter 请求路由器，判断是否需要拦截
type RequestRouter struct {
	interceptDomains      []string
	smartInterceptDomains []string
	mitmAll               bool
}

func NewRequestRouter(interceptDomains, smartInterceptDomains []string, mitmAll bool) *RequestRouter {
	return &RequestRouter{
		interceptDomains:      interceptDomains,
		smartInterceptDomains: smartInterceptDomains,
		mitmAll:               mitmAll,
	}
}

// UpdateDomains 更新拦截域名列表
func (r *RequestRouter) UpdateDomains(interceptDomains, smartInterceptDomains []string) {
	r.interceptDomains = interceptDomains
	r.smartInterceptDomains = smartInterceptDomains
}

// SetMitmAll 设置是否全量 MITM
func (r *RequestRouter) SetMitmAll(v bool) {
	r.mitmAll = v
}

// ShouldIntercept 判断域名是否在直接拦截列表中
func (r *RequestRouter) ShouldIntercept(hostname string) bool {
	for _, d := range r.interceptDomains {
		if hostname == d {
			return true
		}
	}
	return false
}

// IsSmartInterceptDomain 判断域名是否在智能拦截列表中
func (r *RequestRouter) IsSmartInterceptDomain(hostname string) bool {
	for _, d := range r.smartInterceptDomains {
		if hostname == d {
			return true
		}
	}
	return false
}

// ShouldMITM 判断是否需要 MITM 解密
func (r *RequestRouter) ShouldMITM(hostname string) bool {
	if r.mitmAll {
		return true
	}
	return r.ShouldIntercept(hostname) || r.IsSmartInterceptDomain(hostname)
}

// IsLLMRequest 判断请求是否为 LLM 推理请求
func IsLLMRequest(method, path string, headers map[string]string, body []byte) bool {
	// 方法检查：必须是 POST
	if method != "POST" {
		return false
	}

	// 路径检查
	pathLower := strings.ToLower(path)
	for _, llmPath := range llmInferencePaths {
		if pathLower == llmPath || strings.HasPrefix(pathLower, llmPath+"?") || strings.HasPrefix(pathLower, llmPath+"/") {
			return true
		}
	}

	// Content-Type 检查
	contentType := strings.ToLower(headers["content-type"])
	if !strings.Contains(contentType, "application/json") {
		return false
	}

	// 请求体检查
	if len(body) == 0 {
		return false
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false
	}

	// 必须有 model 字段
	if _, ok := parsed["model"]; !ok {
		return false
	}

	// 且包含至少一个 LLM 特征字段
	for _, field := range llmFields {
		if _, ok := parsed[field]; ok {
			return true
		}
	}

	return false
}
