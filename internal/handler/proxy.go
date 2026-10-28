package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/never/zero-api/internal/pricing"
	"github.com/never/zero-api/internal/router"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/adapter"
	"github.com/never/zero-api/internal/diagnostics"
	"github.com/never/zero-api/internal/metrics"
	"github.com/never/zero-api/internal/store"
	"github.com/never/zero-api/internal/upstream"
)

type ProxyHandler struct {
	channelRepo      *store.ChannelRepo
	modelRepo        *store.ModelRepo
	usageRepo        *store.UsageRepo
	tpsMonitor       *metrics.TPSMonitor
	apiKeyRepo       *store.APIKeyRepo
	proxyConfigRepo  *store.ProxyConfigRepo
	virtualModelRepo *store.VirtualModelRepo
	breaker          *CircuitBreaker
	proxyConfigMu    sync.RWMutex
	recorder         diagnostics.Recorder
	proxyConfigCache *store.ProxyConfigData
	modelsCache      []byte    // /v1/models 响应缓存
	modelsCacheTime  time.Time // 缓存时间
	modelsCacheMu    sync.RWMutex
	modelsCacheTTL   time.Duration // 缓存有效期
	// routers 模型路由规则链（虚拟模型等，支持后续扩展新规则）
	routers []router.Rule
}

func NewProxyHandler(channelRepo *store.ChannelRepo, modelRepo *store.ModelRepo, usageRepo *store.UsageRepo, apiKeyRepo *store.APIKeyRepo, proxyConfigRepo *store.ProxyConfigRepo, virtualModelRepo *store.VirtualModelRepo, tpsMonitor *metrics.TPSMonitor) *ProxyHandler {
	h := &ProxyHandler{
		channelRepo:      channelRepo,
		modelRepo:        modelRepo,
		usageRepo:        usageRepo,
		tpsMonitor:       tpsMonitor,
		apiKeyRepo:       apiKeyRepo,
		proxyConfigRepo:  proxyConfigRepo,
		virtualModelRepo: virtualModelRepo,
		breaker:          NewCircuitBreaker(),
		modelsCacheTTL:   60 * time.Second,
	}
	cfg, err := proxyConfigRepo.Get()
	if err == nil {
		h.proxyConfigCache = cfg
	}

	// 构建模型路由规则链：后续新增路由规则在此注册
	vmRouter := router.NewVirtualModelRouter(virtualModelRepo, modelRepo, channelRepo, h.getProxyConfig)
	vmRouter.SetUsageRepo(usageRepo)
	vmRouter.SetAPIKeyRepo(apiKeyRepo)
	h.routers = []router.Rule{
		vmRouter,
	}
	return h
}

// InvalidateModelsCache 清除模型列表响应缓存（模型变更时调用）
func (h *ProxyHandler) InvalidateModelsCache() {
	h.modelsCacheMu.Lock()
	h.modelsCache = nil
	h.modelsCacheMu.Unlock()
}

// getProxyConfig 获取代理配置（优先使用缓存）
func (h *ProxyHandler) getProxyConfig() *store.ProxyConfigData {
	h.proxyConfigMu.Lock()
	defer h.proxyConfigMu.Unlock()
	if h.proxyConfigCache != nil {
		return h.proxyConfigCache
	}
	if h.proxyConfigRepo != nil {
		if cfg, err := h.proxyConfigRepo.Get(); err == nil {
			h.proxyConfigCache = cfg
			return cfg
		}
	}
	return &store.ProxyConfigData{RequestTimeoutSeconds: 60, VisionTimeoutSeconds: 60, FailoverEnabled: true}
}

// ListLocalModels 返回本地启用的模型列表（兼容 OpenAI /v1/models）
// 格式参考 OpenRouter /api/v1/models，返回丰富的模型元信息
// 使用缓存避免频繁 JSON 编码
func modalityFromModalities(input, output []string) string {
	format := func(values []string) string {
		if len(values) == 0 {
			return "text"
		}
		copyValues := append([]string(nil), values...)
		return strings.Join(copyValues, "+")
	}
	return format(input) + "->" + format(output)
}

func (h *ProxyHandler) ListLocalModels(c *gin.Context) {
	// 尝试使用缓存（TTL 60s）
	h.modelsCacheMu.RLock()
	if h.modelsCache != nil && time.Since(h.modelsCacheTime) < h.modelsCacheTTL {
		c.Header("Cache-Control", "public, max-age=60")
		c.Data(http.StatusOK, "application/json", h.modelsCache)
		h.modelsCacheMu.RUnlock()
		return
	}
	h.modelsCacheMu.RUnlock()

	models, err := h.modelRepo.List(0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var data []gin.H
	seenModelIDs := make(map[string]struct{})
	for _, m := range models {
		if m.Status != "active" {
			continue
		}
		// models 已按渠道 priority 排序；同名模型只暴露优先级最高的渠道记录。
		if _, exists := seenModelIDs[m.ModelID]; exists {
			continue
		}
		seenModelIDs[m.ModelID] = struct{}{}

		displayName := m.DisplayName
		if displayName == "" {
			displayName = m.ModelID
		}

		// 构建输入/输出模态
		inputModalities := m.EffectiveInputModalities()
		outputModalities := m.EffectiveOutputModalities()

		// 构建 supported_parameters
		supportedParams := []string{"max_tokens", "temperature", "top_p", "seed", "stop", "response_format", "structured_outputs"}
		if m.SupportsTools {
			supportedParams = append(supportedParams, "tools", "tool_choice")
		}
		if m.SupportsThinking {
			supportedParams = append(supportedParams, "reasoning", "include_reasoning")
		}

		// 构建 pricing（OpenRouter 格式：每 token 价格的字符串表示）
		pricing := gin.H{
			"prompt":     fmt.Sprintf("%.9f", m.PricingInput/1000000),
			"completion": fmt.Sprintf("%.9f", m.PricingOutput/1000000),
		}
		if m.PricingCacheRead > 0 {
			pricing["input_cache_read"] = fmt.Sprintf("%.9f", m.PricingCacheRead/1000000)
		}
		if m.PricingCacheWrite > 0 {
			pricing["input_cache_write"] = fmt.Sprintf("%.9f", m.PricingCacheWrite/1000000)
		}

		// 构建 default_parameters（OpenRouter 格式）
		defaultParams := gin.H{
			"temperature":        nil,
			"top_p":              nil,
			"top_k":              nil,
			"frequency_penalty":  nil,
			"presence_penalty":   nil,
			"repetition_penalty": nil,
		}

		entry := gin.H{
			"id":             m.ModelID,
			"object":         "model",
			"created":        m.CreatedAt.Unix(),
			"owned_by":       "zero-api",
			"name":           displayName,
			"description":    fmt.Sprintf("zero-api model: %s via %s", m.ModelID, m.ChannelName),
			"context_length": m.ContextWindow,
			"architecture": gin.H{
				"modality":          modalityFromModalities(inputModalities, outputModalities),
				"input_modalities":  inputModalities,
				"output_modalities": outputModalities,
				"tokenizer":         "Custom",
				"instruct_type":     nil,
			},
			"pricing": pricing,
			"top_provider": gin.H{
				"context_length":        m.ContextWindow,
				"max_completion_tokens": m.MaxOutputTokens,
				"is_moderated":          false,
			},
			"per_request_limits":   nil,
			"supported_parameters": supportedParams,
			"default_parameters":   defaultParams,
			"supported_voices":     nil,
			"knowledge_cutoff":     nil,
			"expiration_date":      nil,
		}
		if len(m.Capabilities) > 0 {
			entry["capabilities"] = m.Capabilities
			entry["input_modalities"] = m.EffectiveInputModalities()
			entry["output_modalities"] = m.EffectiveOutputModalities()
		}

		// reasoning 字段（OpenRouter 格式）
		if m.SupportsThinking {
			entry["reasoning"] = gin.H{
				"mandatory":       false,
				"default_enabled": true,
			}
		}
		if m.SupportsCapability("image_generation") || m.SupportsCapability("image_editing") {
			entry["capabilities"] = m.Capabilities
			entry["input_modalities"] = m.EffectiveInputModalities()
			entry["output_modalities"] = m.EffectiveOutputModalities()
		}

		data = append(data, entry)
	}

	// 合并虚拟模型（模型路由）：以主模型的能力为基础构建条目
	// 虚拟模型在 /v1/models 中展示为 supports_vision=true（因为识图扩展让下游"能看图"）
	if vms, verr := h.virtualModelRepo.List(); verr == nil {
		modelByName := make(map[string]*store.Model)
		for i := range models {
			modelByName[models[i].ModelID] = &models[i]
		}
		for _, vm := range vms {
			if vm.Status != "active" {
				continue
			}
			main := modelByName[vm.MainModel]
			if main == nil {
				continue
			}
			displayName := vm.DisplayName
			if displayName == "" {
				displayName = vm.Name
			}
			// 虚拟模型继承主模型的能力：工具调用 / 思考 / 视觉
			vmParams := []string{"max_tokens", "temperature", "top_p", "seed", "stop", "response_format", "structured_outputs"}
			if main.SupportsTools {
				vmParams = append(vmParams, "tools", "tool_choice")
			}
			if main.SupportsThinking {
				vmParams = append(vmParams, "reasoning", "include_reasoning")
			}
			// 视觉能力：配置了识图模型（识图扩展）或主模型本身支持视觉时，宣称支持 image
			// 否则按纯文本声明（避免下游误认为可识图）
			inputMods := []string{"text"}
			if vm.VisionModel != "" || main.SupportsVision {
				inputMods = []string{"text", "image"}
			}
			entry := gin.H{
				"id":             vm.Name,
				"object":         "model",
				"name":           displayName,
				"created":        main.CreatedAt.Unix(),
				"owned_by":       "zero-api",
				"description":    fmt.Sprintf("虚拟模型: 路由到 %s（识图扩展: %s）", vm.MainModel, vm.VisionModel),
				"context_length": main.ContextWindow,
				"architecture": gin.H{
					"modality":          modalityFromModalities(inputMods, []string{"text"}),
					"input_modalities":  inputMods,
					"output_modalities": []string{"text"},
					"tokenizer":         "Custom",
					"instruct_type":     nil,
				},
				"pricing": gin.H{
					"prompt":     fmt.Sprintf("%.9f", main.PricingInput/1000000),
					"completion": fmt.Sprintf("%.9f", main.PricingOutput/1000000),
				},
				"top_provider": gin.H{
					"context_length":        main.ContextWindow,
					"max_completion_tokens": main.MaxOutputTokens,
					"is_moderated":          false,
				},
				"per_request_limits":   nil,
				"supported_parameters": vmParams,
				"default_parameters":   gin.H{},
				"supported_voices":     nil,
				"knowledge_cutoff":     nil,
				"expiration_date":      nil,
			}
			// reasoning 字段（与普通模型一致）
			if main.SupportsThinking {
				entry["reasoning"] = gin.H{
					"mandatory":       false,
					"default_enabled": true,
				}
			}
			data = append(data, entry)
		}
	}

	if data == nil {
		data = []gin.H{}
	}
	c.Header("Cache-Control", "public, max-age=60")
	body, _ := json.Marshal(gin.H{
		"object": "list",
		"data":   data,
	})
	// 写入缓存
	h.modelsCacheMu.Lock()
	h.modelsCache = body
	h.modelsCacheTime = time.Now()
	h.modelsCacheMu.Unlock()
	c.Data(http.StatusOK, "application/json", body)
}

// ChatCompletion 处理 OpenAI 兼容聊天补全请求（下游 OpenAI 协议）
func (h *ProxyHandler) ChatCompletion(c *gin.Context) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求体失败"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	h.handleCompletion(c, bodyBytes, adapter.NewDownstreamAdapter("openai"))
}

// MessagesCompletion 处理 Anthropic Messages 请求（下游 Anthropic 协议）
func (h *ProxyHandler) MessagesCompletion(c *gin.Context) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求体失败"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	h.handleCompletion(c, bodyBytes, adapter.NewDownstreamAdapter("anthropic"))
}

// ResponsesCompletion 处理 OpenAI Responses API 请求（下游 Responses 协议）
func (h *ProxyHandler) ResponsesCompletion(c *gin.Context) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求体失败"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	h.handleCompletion(c, bodyBytes, adapter.NewDownstreamAdapter("responses"))
}

// PassthroughEndpoint 功能类接口透传（embeddings / images / audio / moderations / batches 等）
// 这些接口使用 OpenAI 标准格式且各供应商兼容，无需协议转换：
//   - 请求体原样转发到上游（OpenAI 兼容渠道）
//   - 响应原样返回
//   - 仅支持 openai 类型渠道（协议转换类渠道不适用）
func (h *ProxyHandler) PassthroughEndpoint(c *gin.Context) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求体失败"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 解析请求体获取模型名。JSON 接口从顶层字段读取；文件上传接口
	// 使用 multipart/form-data，model 位于普通表单字段中。
	model, err := passthroughRequestModel(c.Request.Header.Get("Content-Type"), bodyBytes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 model 字段"})
		return
	}

	// 验证并解析 API Key（含额度校验）
	apiKey, err := h.resolveAndValidateAPIKey(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	apiKeyID := &apiKey.ID

	// 模型访问校验（allowed_models 限制）
	if err := h.checkAPIKeyModelAccess(apiKey, model); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	allModels, err := h.modelRepo.List(0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询模型失败"})
		return
	}

	// 查找启用的匹配模型（仅支持 openai 协议的模型支持功能类接口透传）
	var candidates []*store.Model
	for i, m := range allModels {
		if m.ModelID == model && m.Status == "active" {
			ch, cerr := h.channelRepo.GetByID(m.ChannelID)
			if cerr != nil || ch.Status != "active" || !m.SupportsProtocol("openai", ch.Type) {
				continue
			}
			capability := imageCapabilityForPath(c.Request.URL.Path)
			if capability != "" && !m.SupportsCapability(capability) {
				continue
			}
			candidates = append(candidates, &allModels[i])
		}
	}
	if len(candidates) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("模型 %s 未找到、未启用或渠道类型不支持功能类接口（需 OpenAI 兼容渠道）", model)})
		return
	}

	proxyConfig := h.getProxyConfig()
	requestTimeout := time.Duration(proxyConfig.RequestTimeoutSeconds) * time.Second

	// 按优先级依次尝试
	for _, matchedModel := range candidates {
		if c.Request.Context().Err() != nil {
			return
		}
		c.Set("forward_attempt", c.GetInt("forward_attempt")+1)
		ch, err := h.channelRepo.GetByID(matchedModel.ChannelID)
		if err != nil {
			h.recordForwardAttempt(c, matchedModel, &store.Channel{ID: matchedModel.ChannelID}, "openai", false, time.Now(), c.Writer.Size(), 0, "", forwardingFailure("internal", err, false, false))
			continue
		}
		if ch.Status != "active" {
			continue
		}

		if err := h.tryForwardPassthrough(c, bodyBytes, c.Request.Header.Get("Content-Type"), matchedModel, ch, apiKeyID, requestTimeout, proxyConfig); err == nil {
			h.breaker.RecordSuccess(ch.ID)
			return
		} else {
			if !h.finishForwardFailure(c, ch, err) {
				return
			}
		}
	}

	c.JSON(http.StatusBadGateway, gin.H{"error": "All upstream channels failed"})
}

// passthroughRequestModel extracts the model field without altering the original body.
func passthroughRequestModel(contentType string, body []byte) (string, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", fmt.Errorf("无效的 Content-Type: %w", err)
	}

	switch strings.ToLower(mediaType) {
	case "application/json":
		var reqBody struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(body, &reqBody); err != nil {
			return "", fmt.Errorf("请求 JSON 格式无效: %w", err)
		}
		return strings.TrimSpace(reqBody.Model), nil
	case "multipart/form-data":
		boundary := params["boundary"]
		if boundary == "" {
			return "", errors.New("multipart 请求缺少 boundary")
		}
		form, err := multipart.NewReader(bytes.NewReader(body), boundary).ReadForm(32 << 20)
		if err != nil {
			return "", fmt.Errorf("解析 multipart 请求失败: %w", err)
		}
		defer form.RemoveAll()
		values := form.Value["model"]
		if len(values) == 0 {
			return "", nil
		}
		return strings.TrimSpace(values[0]), nil
	default:
		return "", fmt.Errorf("不支持的请求 Content-Type: %s", mediaType)
	}
}

// tryForwardPassthrough 功能类接口透传：请求体原样转发，响应原样返回
func (h *ProxyHandler) tryForwardPassthrough(c *gin.Context, bodyBytes []byte, contentType string, matchedModel *store.Model, ch *store.Channel, apiKeyID *int64, requestTimeout time.Duration, proxyConfig *store.ProxyConfigData) (result error) {
	startTime := time.Now()
	initialBytes := c.Writer.Size()
	upstreamStatus := 0
	upstreamID := ""
	defer func() {
		if result != nil {
			result = forwardOutcome(result, c.Writer.Written())
		}
		h.recordForwardAttempt(c, matchedModel, ch, "openai", false, startTime, initialBytes, upstreamStatus, upstreamID, result)
	}()
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})
	requestTimeout = forwardTimeout(requestTimeout)
	adapt := adapter.NewAdapter(ch.Type)

	// 功能类接口上游 URL 与聊天不同：/v1/embeddings、/v1/images/... 等
	// 客户端请求路径即上游路径（OpenAI 兼容格式）
	upstreamURL := adapt.GetChatURL(ch.BaseURL)
	_ = upstreamURL

	// 构造上游 URL：base + 客户端请求路径（保留 /v1 之后的路径）
	path := c.Request.URL.Path
	base := strings.TrimRight(ch.BaseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		base = strings.TrimSuffix(base, "/v1")
	}
	upstreamURL = base + path

	var cancel context.CancelFunc
	upstreamCtx, cancel := context.WithTimeout(c.Request.Context(), forwardTimeout(requestTimeout))
	defer cancel()

	req, err := http.NewRequestWithContext(upstreamCtx, "POST", upstreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return forwardingFailure("internal", err, false, false)
	}
	if contentType == "" {
		return &forwardError{category: "invalid_request", status: http.StatusBadRequest}
	}
	req.Header.Set("Content-Type", contentType)
	forwardRequestID(c, req)

	// 认证头
	if ch.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+ch.APIKey)
	} else if auth := c.GetHeader("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	var client *http.Client
	if ch.UseProxy && proxyConfig.ForwardProxyURL != "" {
		client, err = upstream.NewHTTPClientWithProxyAndTimeout(
			proxyConfig.ForwardProxyURL,
			proxyConfig.ForwardProxyUser,
			proxyConfig.ForwardProxyPass,
			requestTimeout,
		)
		if err != nil {
			return forwardingFailure("internal", err, false, false)
		}
	}
	if client == nil {
		client = upstream.NewHTTPClientWithTimeout(requestTimeout)
	}

	resp, err := client.Do(req)
	if err != nil {
		return transportFailure(c.Request.Context(), err)
	}
	defer resp.Body.Close()

	upstreamStatus = resp.StatusCode
	upstreamID = resp.Header.Get("X-Request-Id")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return upstreamStatusFailure(c, resp, requestTimeout)
	}

	latencyMs := int(time.Since(startTime).Milliseconds())
	totalDurationMs := latencyMs
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return transportFailure(c.Request.Context(), err)
	}

	// 记录用量（功能类接口响应可能含 usage，尝试提取）
	if h.usageRepo != nil {
		go h.recordUsage(matchedModel.ModelID, respBytes, respBytes, adapt, nil, matchedModel, ch.ID, apiKeyID, latencyMs, totalDurationMs)
	}

	copyForwardHeaders(c, resp.Header, true)
	return writeForwardBody(c, resp.StatusCode, resp.Header.Get("Content-Type"), respBytes, requestTimeout)
}

func imageCapabilityForPath(path string) string {
	path = strings.ToLower(strings.TrimSpace(path))
	switch {
	case strings.HasSuffix(path, "/images/generations"):
		return "image_generation"
	case strings.HasSuffix(path, "/images/edits"):
		return "image_editing"
	default:
		return ""
	}
}

// handleCompletion 核心中转逻辑（下游协议 → 规范格式 → 上游渠道）
func (h *ProxyHandler) handleCompletion(c *gin.Context, rawBody []byte, downstream adapter.DownstreamAdapter) {
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})
	var reqBody struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(rawBody, &reqBody); err != nil || reqBody.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 model 字段"})
		return
	}
	// 验证并解析 API Key（含额度校验）
	apiKey, err := h.resolveAndValidateAPIKey(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	apiKeyID := &apiKey.ID

	// 模型访问校验（allowed_models 限制）
	if err := h.checkAPIKeyModelAccess(apiKey, reqBody.Model); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	// 模型路由规则（虚拟模型等）：命中规则时转换请求或直接处理
	// 规则链可扩展：后续新路由规则注册到 h.routers 即可
	// 流式预响应：规则在耗时操作（识图）前先向客户端发送提示帧，
	// 避免下游长时间无响应（仅 OpenAI chat 协议支持，透传不适用）
	var streamPreface func(model, content string) error
	if reqBody.Stream && downstream.Protocol() == "openai" && !downstream.IsPassthrough() {
		streamPreface = h.writeStreamPreface(c, reqBody.Model)
	}
	if res := router.ApplyRules(h.routers, &router.Context{
		Protocol:      downstream.Protocol(),
		RawBody:       rawBody,
		Model:         reqBody.Model,
		APIKeyID:      apiKeyID,
		ClientAuth:    c.GetHeader("Authorization"),
		Stream:        reqBody.Stream,
		StreamPreface: streamPreface,
	}); res != nil {
		if res.Handled {
			// 规则已完全接管（写响应）
			c.Data(res.StatusCode, "application/json", res.RespBody)
			return
		}
		if res.NewBody != nil {
			// 规则转换了请求体（如模型名替换/图片替换），继续走普通链路
			rawBody = res.NewBody
			var rb struct {
				Model  string `json:"model"`
				Stream bool   `json:"stream"`
			}
			if err := json.Unmarshal(rawBody, &rb); err == nil && rb.Model != "" {
				reqBody = rb
			}
		}
	}

	allModels, err := h.modelRepo.List(0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询模型失败"})
		return
	}

	// 按渠道优先级查找所有启用的匹配模型（已按 c.priority 排序）
	var candidates []*store.Model
	for i, m := range allModels {
		if m.ModelID == reqBody.Model && m.Status == "active" {
			candidates = append(candidates, &allModels[i])
		}
	}
	if len(candidates) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("模型 %s 未找到或未启用", reqBody.Model)})
		return
	}

	// 读取 proxyConfig（用于超时和探测 API Key）—— 使用缓存减少 DB 查询
	proxyConfig := h.getProxyConfig()
	requestTimeout := time.Duration(proxyConfig.RequestTimeoutSeconds) * time.Second

	// 按优先级依次尝试，失败时自动切换到下一渠道（含熔断逻辑）
	var lastErr error
	var breakerSkipped bool // 标记是否有渠道因熔断冷却被跳过
	var retryAfter time.Duration
	for _, matchedModel := range candidates {
		if c.Request.Context().Err() != nil {
			return
		}
		c.Set("forward_attempt", c.GetInt("forward_attempt")+1)
		// 获取渠道
		ch, err := h.channelRepo.GetByID(matchedModel.ChannelID)
		if err != nil {
			lastErr = forwardingFailure("internal", err, false, false)
			h.recordForwardAttempt(c, matchedModel, &store.Channel{ID: matchedModel.ChannelID}, downstream.Protocol(), reqBody.Stream, time.Now(), c.Writer.Size(), 0, "", lastErr)
			continue
		}
		if ch.Status != "active" {
			continue
		}

		// 熔断检查（全局开关 + 渠道开关）
		if proxyConfig.FailoverEnabled && ch.FailoverEnabled {
			allow, needProbe := h.breaker.MayProceed(ch.ID)
			if !allow && !needProbe {
				breakerSkipped = true
				delay := h.breaker.RetryAfter(ch.ID)
				if delay <= 0 {
					delay = time.Second
				}
				if retryAfter == 0 || delay < retryAfter {
					retryAfter = delay
				}
				h.recordForwardAttempt(c, matchedModel, ch, downstream.Protocol(), reqBody.Stream, time.Now(), c.Writer.Size(), 0, "", &forwardError{category: "circuit_open", status: http.StatusServiceUnavailable})
				continue
			}
			if needProbe {
				probeStarted := time.Now()
				forwardURL, forwardUser, forwardPass := "", "", ""
				if ch.UseProxy && proxyConfig.ForwardProxyURL != "" {
					forwardURL = proxyConfig.ForwardProxyURL
					forwardUser = proxyConfig.ForwardProxyUser
					forwardPass = proxyConfig.ForwardProxyPass
				}
				if err := h.breaker.ProbeModelAndRecoverContext(c.Request.Context(), ch, matchedModel, downstream.Protocol(), proxyConfig.ProbeAPIKey, forwardURL, forwardUser, forwardPass, requestTimeout); err != nil {
					outcome := &forwardError{category: "probe_failed", status: http.StatusServiceUnavailable}
					if c.Request.Context().Err() != nil {
						outcome = &forwardError{category: "client_cancelled", clientCancelled: true}
					}
					h.recordForwardAttempt(c, matchedModel, ch, downstream.Protocol(), reqBody.Stream, probeStarted, c.Writer.Size(), 0, "", outcome)
					if outcome.clientCancelled {
						return
					}
					breakerSkipped = true
					delay := h.breaker.RetryAfter(ch.ID)
					if delay <= 0 {
						delay = time.Second
					}
					if retryAfter == 0 || delay < retryAfter {
						retryAfter = delay
					}
					continue
				}
				// 探测通过，继续执行 tryForward
			}
		}

		if err := h.tryForward(c, rawBody, matchedModel, ch, apiKeyID, requestTimeout, proxyConfig, reqBody.Stream, downstream); err == nil {
			h.breaker.RecordSuccess(ch.ID)
			return
		} else {
			lastErr = err
			if !h.finishForwardFailure(c, ch, err) {
				return
			}
		}
	}

	if breakerSkipped && lastErr == nil {
		if retryAfter <= 0 {
			retryAfter = time.Second
		}
		c.Header("Retry-After", fmt.Sprint(int64((retryAfter+time.Second-1)/time.Second)))
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "All model channels are temporarily unavailable"})
		return
	}

	c.JSON(http.StatusBadGateway, gin.H{"error": "All upstream channels failed"})
}

// InvalidateProxyConfig 清除代理配置缓存和模型列表缓存（设置页面保存后调用）
func (h *ProxyHandler) InvalidateProxyConfig() {
	h.proxyConfigMu.Lock()
	h.proxyConfigCache = nil
	h.proxyConfigMu.Unlock()
	h.InvalidateModelsCache()
}

// resolveAndValidateAPIKey 从请求中提取并验证 API Key
// 返回 APIKey 完整对象（含额度/模型配置），如果验证失败则返回 error
// 校验项：key 存在且启用、额度已启用时余额 > 0
func (h *ProxyHandler) resolveAndValidateAPIKey(c *gin.Context) (*store.APIKey, error) {
	auth := c.GetHeader("Authorization")
	if auth == "" {
		return nil, fmt.Errorf("缺少 Authorization 头，请提供有效的 API Key")
	}

	parts := splitAuth(auth)
	if parts == nil || parts[0] != "Bearer" {
		return nil, fmt.Errorf("Authorization 格式错误，需 Bearer <api-key>")
	}

	k, err := h.apiKeyRepo.GetByKey(parts[1])
	if err != nil {
		return nil, fmt.Errorf("无效的 API Key：密钥不存在或已被禁用")
	}

	// 额度校验：启用额度且余额 <= 0 时拒绝
	if k.QuotaEnabled && k.QuotaBalance <= 0 {
		return nil, fmt.Errorf("API Key 额度已用尽，请联系管理员充值")
	}

	return k, nil
}

// checkAPIKeyModelAccess 校验 API Key 是否允许使用指定模型
// allowed_models 为空表示全部允许
func (h *ProxyHandler) checkAPIKeyModelAccess(ak *store.APIKey, model string) error {
	if ak == nil || ak.AllowedModels == "" || ak.AllowedModels == "[]" || ak.AllowedModels == "null" {
		return nil // 默认不限制
	}
	var allowed []string
	if err := json.Unmarshal([]byte(ak.AllowedModels), &allowed); err != nil {
		return nil // 解析失败视为不限制
	}
	for _, m := range allowed {
		if m == model {
			return nil
		}
	}
	return fmt.Errorf("API Key 未被授权使用模型 %s", model)
}

// parseAllowedModels 解析允许的模型列表（供其他 handler 使用）
func parseAllowedModels(s string) []string {
	if s == "" || s == "[]" || s == "null" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func (h *ProxyHandler) recordUsage(requestModel string, rawResp, convertedResp []byte, adapt, usageAdapt adapter.Adapter, model *store.Model, channelID int64, apiKeyID *int64, latencyMs int, totalDurationMs int) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Usage] 记录用量 panic 恢复: %v", r)
		}
	}()

	modelID := model.ID
	var promptTokens, completionTokens, cacheHitTokens, totalTokens int
	var cost float64

	// 从响应中提取用量：透传时使用真实下游协议适配器，转换时使用渠道适配器
	if usageAdapt == nil {
		usageAdapt = adapt
	}
	usage, err := usageAdapt.ExtractUsage(convertedResp)
	if err != nil && usageAdapt != adapt {
		usage, err = adapt.ExtractUsage(convertedResp)
	}
	if err != nil {
		usage, err = usageAdapt.ExtractUsage(rawResp)
	}
	if err != nil && usageAdapt != adapt {
		usage, err = adapt.ExtractUsage(rawResp)
	}
	if err != nil {
		// 兜底：兼容历史响应格式
		if u, uerr := (&adapter.ResponsesAdapter{}).ExtractUsage(rawResp); uerr == nil {
			usage, err = u, nil
		} else if u, uerr := (&adapter.AnthropicAdapter{}).ExtractUsage(rawResp); uerr == nil {
			usage, err = u, nil
		} else if u, uerr := (&adapter.OpenAIAdapter{}).ExtractUsage(rawResp); uerr == nil {
			usage, err = u, nil
		}
	}
	if err != nil {
		log.Printf("[Usage] ExtractUsage 失败 (model=%s): %v — 仍记录请求", requestModel, err)
	} else {
		promptTokens = usage.PromptTokens
		completionTokens = usage.CompletionTokens
		cacheHitTokens = usage.CacheHitTokens
		totalTokens = usage.TotalTokens

		// 解析定价规则，无规则时使用 flat 定价
		flat := pricing.PricingSet{
			Input:      model.PricingInput,
			Output:     model.PricingOutput,
			CacheRead:  model.PricingCacheRead,
			CacheWrite: model.PricingCacheWrite,
		}
		_, resolved := pricing.ResolvePricing(
			model.ParsedPricingRules(),
			flat,
			time.Now().UTC(),
			usage.PromptTokens,
			usage.TotalTokens,
		)

		cacheMissTokens := usage.PromptTokens - usage.CacheHitTokens
		cost = (float64(cacheMissTokens)/1000000)*resolved.Input +
			(float64(usage.CacheHitTokens)/1000000)*resolved.CacheRead +
			(float64(usage.CompletionTokens)/1000000)*resolved.Output
	}

	if _, err := h.usageRepo.Insert(&store.UsageRecord{
		ChannelID:        &channelID,
		ModelID:          &modelID,
		APIKeyID:         apiKeyID,
		RequestModel:     requestModel,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CacheHitTokens:   cacheHitTokens,
		TotalTokens:      totalTokens,
		LatencyMs:        latencyMs,
		TotalDurationMs:  totalDurationMs,
		Cost:             cost,
	}); err != nil {
		log.Printf("[Usage] 插入记录失败: %v", err)
	}

	// 扣减 API Key 额度（启用额度的 key 按实际费用扣减）
	if apiKeyID != nil && cost > 0 {
		if _, err := h.apiKeyRepo.DeductQuota(*apiKeyID, cost); err != nil {
			log.Printf("[Quota] API Key %d 扣减额度失败: %v", *apiKeyID, err)
		}
	}
}

// tryForward 尝试将请求转发到指定渠道，成功返回 nil，失败返回 error
// rawBody 为客户端原始请求（下游协议格式），downstream 为下游协议适配器
func (h *ProxyHandler) tryForward(c *gin.Context, rawBody []byte, matchedModel *store.Model, ch *store.Channel, apiKeyID *int64, requestTimeout time.Duration, proxyConfig *store.ProxyConfigData, isStream bool, downstream adapter.DownstreamAdapter) (result error) {
	startTime := time.Now()
	initialBytes := c.Writer.Size()
	initialStarted := c.Writer.Written()
	upstreamStatus := 0
	upstreamID := ""
	downstreamProtocol := downstream.Protocol()
	defer func() {
		if result != nil {
			outcome := forwardOutcome(result, c.Writer.Written())
			if isStream && initialStarted && !outcome.streamReported && !outcome.clientCancelled && outcome.category != "downstream_write" {
				writeCommittedStreamError(c, downstreamProtocol, outcome.category, requestTimeout)
				outcome.streamReported = true
			}
			result = outcome
		}
		h.recordForwardAttempt(c, matchedModel, ch, downstreamProtocol, isStream, startTime, initialBytes, upstreamStatus, upstreamID, result)
	}()
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})
	requestTimeout = forwardTimeout(requestTimeout)
	if c.Request.Context().Err() != nil {
		return transportFailure(c.Request.Context(), c.Request.Context().Err())
	}
	adapt := adapter.NewAdapter(ch.Type)
	if adapter.CanPassthrough(ch.Type, downstreamProtocol, ch.SupportsProtocol(downstreamProtocol), matchedModel.SupportsProtocol(downstreamProtocol, ch.Type)) {
		downstream = adapter.NewPassthroughDownstreamAdapter(downstreamProtocol)
	}
	var bodyBytes []byte
	if downstream.IsPassthrough() {
		bodyBytes = rawBody
		if downstreamProtocol == "responses" {
			bodyBytes = adapter.NormalizeResponsesRequest(bodyBytes)
		}
	} else {
		canonical, err := downstream.RequestToCanonical(rawBody)
		if err != nil {
			return &forwardError{category: "conversion_error", status: http.StatusBadRequest, cause: err}
		}
		converted, err := adapt.ConvertRequest(matchedModel.ModelID, canonical)
		if err != nil {
			return &forwardError{category: "conversion_error", status: http.StatusBadRequest, cause: err}
		}
		bodyBytes = converted
	}
	upstreamURL := adapt.GetChatURL(ch.BaseURL)
	if downstream.IsPassthrough() {
		upstreamURL = matchedModel.ProtocolURL(downstreamProtocol, ch.BaseURL)
	}
	if ch.Type == "gemini" {
		endpoint := "generateContent"
		if isStream {
			endpoint = "streamGenerateContent"
		}
		upstreamURL = fmt.Sprintf("%s/%s:%s", upstreamURL, matchedModel.ModelID, endpoint)
		query := url.Values{}
		if isStream {
			query.Set("alt", "sse")
		}
		if ch.APIKey != "" {
			query.Set("key", ch.APIKey)
		}
		if len(query) > 0 {
			upstreamURL += "?" + query.Encode()
		}
	}
	var upstreamCtx context.Context
	var cancel context.CancelFunc
	if isStream {
		upstreamCtx, cancel = context.WithCancel(c.Request.Context())
	} else {
		upstreamCtx, cancel = context.WithTimeout(c.Request.Context(), requestTimeout)
	}
	defer cancel()
	req, err := http.NewRequestWithContext(upstreamCtx, "POST", upstreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return forwardingFailure("internal", err, false, false)
	}
	req.Header.Set("Content-Type", "application/json")
	forwardRequestID(c, req)
	if downstream.IsPassthrough() && downstreamProtocol == "responses" {
		adapter.CopyResponsesSessionHeaders(c.Request.Header, req.Header.Add)
	}
	if ch.Type == "anthropic" {
		key := ch.APIKey
		if key == "" {
			key = c.GetHeader("x-api-key")
		}
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if ch.Type != "gemini" {
		if ch.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+ch.APIKey)
		} else if auth := c.GetHeader("Authorization"); auth != "" {
			req.Header.Set("Authorization", auth)
		}
	}
	var client *http.Client
	if ch.UseProxy && proxyConfig.ForwardProxyURL != "" {
		if isStream {
			client, err = upstream.NewStreamHTTPClientWithProxyAndTimeout(proxyConfig.ForwardProxyURL, proxyConfig.ForwardProxyUser, proxyConfig.ForwardProxyPass, requestTimeout)
		} else {
			client, err = upstream.NewHTTPClientWithProxyAndTimeout(proxyConfig.ForwardProxyURL, proxyConfig.ForwardProxyUser, proxyConfig.ForwardProxyPass, requestTimeout)
		}
		if err != nil {
			return forwardingFailure("internal", err, false, false)
		}
	}
	if client == nil {
		if isStream {
			client = upstream.NewStreamHTTPClientWithTimeout(requestTimeout)
		} else {
			client = upstream.NewHTTPClientWithTimeout(requestTimeout)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return transportFailure(c.Request.Context(), err)
	}
	defer resp.Body.Close()
	upstreamStatus = resp.StatusCode
	upstreamID = resp.Header.Get("X-Request-Id")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return upstreamStatusFailure(c, resp, requestTimeout)
	}
	if isStream {
		mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if !strings.EqualFold(mediaType, "text/event-stream") {
			return forwardingFailure("upstream_stream", nil, true, true)
		}
		return h.streamResponse(c, resp, adapt, matchedModel, ch, apiKeyID, startTime, requestTimeout, downstream)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return transportFailure(c.Request.Context(), err)
	}
	semantic := nonstreamSemanticOutcome(raw)
	if semantic != nil && semantic.category == "upstream_error" && !downstream.IsPassthrough() {
		return semantic
	}
	converted := raw
	if !downstream.IsPassthrough() {
		converted, err = adapt.ConvertResponse(raw)
		if err != nil {
			return forwardingFailure("conversion_error", err, false, false)
		}
		converted, err = downstream.ResponseToDownstream(converted)
		if err != nil {
			return forwardingFailure("conversion_error", err, false, false)
		}
	}
	latency := int(time.Since(startTime).Milliseconds())
	if h.usageRepo != nil {
		usageAdapt := adapter.SelectUsageAdapter(adapt, downstream.IsPassthrough(), downstreamProtocol)
		go h.recordUsage(matchedModel.ModelID, raw, converted, adapt, usageAdapt, matchedModel, ch.ID, apiKeyID, latency, latency)
	}
	copyForwardHeaders(c, resp.Header, true)
	if err := writeForwardBody(c, resp.StatusCode, resp.Header.Get("Content-Type"), converted, requestTimeout); err != nil {
		return err
	}
	if semantic != nil {
		semantic.started = c.Writer.Written()
		return semantic
	}
	return nil
}

// streamResponse 流式转发上游 SSE 响应
func (h *ProxyHandler) streamResponse(c *gin.Context, resp *http.Response, adapt adapter.Adapter, matchedModel *store.Model, ch *store.Channel, apiKeyID *int64, startTime time.Time, requestTimeout time.Duration, downstream adapter.DownstreamAdapter) error {
	return h.forwardSSE(c, resp, adapt, matchedModel, ch, apiKeyID, startTime, requestTimeout, downstream)
}

func (h *ProxyHandler) recordStreamTPS(matchedModel *store.Model, observer metrics.TPSSampleObserver, completedAt time.Time) {
	if h.tpsMonitor == nil || matchedModel == nil || observer.OutputTokens <= 0 || observer.FirstOutputAt.IsZero() {
		return
	}
	// 虚拟模型已在路由规则中解析；按实际转发模型归并速度。
	h.tpsMonitor.Record(matchedModel.ModelID, observer.OutputTokens, observer.FirstOutputAt, completedAt)
}

// logResponsesRequestShape 在调试模式下记录 Responses input 结构，不记录用户内容或工具参数。
func logResponsesRequestShape(prefix string, body []byte) {
	if os.Getenv("ZERO_API_DEBUG_RESPONSES") != "1" {
		return
	}
	var payload struct {
		PreviousResponseID string `json:"previous_response_id"`
		Input              []struct {
			Type             string          `json:"type"`
			ID               string          `json:"id"`
			CallID           string          `json:"call_id"`
			Summary          json.RawMessage `json:"summary"`
			EncryptedContent json.RawMessage `json:"encrypted_content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("%s 请求体无法解析: %v", prefix, err)
		return
	}
	log.Printf("%s previous_response_id=%t input_items=%d", prefix, payload.PreviousResponseID != "", len(payload.Input))
	for i, item := range payload.Input {
		log.Printf("%s input[%d]: type=%s id=%t call_id=%t summary=%t encrypted_content=%t", prefix, i, item.Type, item.ID != "", item.CallID != "", len(item.Summary) > 0 && string(item.Summary) != "null", len(item.EncryptedContent) > 0 && string(item.EncryptedContent) != "null")
	}
}

// splitAuth 解析 Authorization 头
func splitAuth(auth string) []string {
	for i := 0; i < len(auth); i++ {
		if auth[i] == ' ' {
			if i+1 < len(auth) {
				return []string{auth[:i], auth[i+1:]}
			}
			return []string{auth[:i], ""}
		}
	}
	return nil
}

// isHopByHop 判断是否为逐跳头，不应转发给客户端
var hopByHopHeaders = map[string]bool{
	"transfer-encoding":   true,
	"connection":          true,
	"keep-alive":          true,
	"te":                  true,
	"trailer":             true,
	"upgrade":             true,
	"proxy-authorization": true,
	"proxy-authenticate":  true,
}

func isHopByHop(key string) bool {
	return hopByHopHeaders[key]
}

// filterHopByHop 筛除逐跳头，返回安全可转发给客户端的头
func filterHopByHop(headers map[string][]string) map[string][]string {
	result := make(map[string][]string, len(headers))
	for k, vals := range headers {
		if !isHopByHop(strings.ToLower(k)) {
			result[k] = vals
		}
	}
	return result
}

// writeStreamPreface 构造流式预响应回调（OpenAI chat 协议 SSE 帧）
// 在识图等耗时操作前向客户端发送提示帧，避免下游长时间无响应。
// 返回的回调负责：设置 SSE 响应头 → 写入并冲刷一个 chat.completion.chunk 帧。
// 注意：之后 streamResponse 会再次设置响应头/状态码（已发送则无效，无害）。
func (h *ProxyHandler) writeStreamPreface(c *gin.Context, model string) func(model, content string) error {
	return func(displayModel, content string) error {
		// 设置 SSE 响应头（prelude 仅调用一次；之后 streamResponse 的设置已发送无效，无害）
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Status(http.StatusOK)
		// 清除 http.Server.WriteTimeout（流式可能持续较长时间）
		http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})

		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			return fmt.Errorf("ResponseWriter 不支持 Flusher")
		}

		chunk := map[string]interface{}{
			"id":      "chatcmpl-" + time.Now().Format("150405"),
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   displayModel,
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"delta":         map[string]interface{}{"role": "assistant", "content": content},
					"finish_reason": nil,
				},
			},
		}
		data, err := json.Marshal(chunk)
		if err != nil {
			return fmt.Errorf("序列化预响应失败: %w", err)
		}

		if _, err := c.Writer.Write([]byte("data: ")); err != nil {
			return err
		}
		if _, err := c.Writer.Write(data); err != nil {
			return err
		}
		if _, err := c.Writer.Write([]byte("\n\n")); err != nil {
			return err
		}
		flusher.Flush()
		log.Printf("[流式] 预响应已发送: %q", content)
		return nil
	}
}
