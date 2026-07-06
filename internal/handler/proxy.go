package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/adapter"
	"github.com/never/zero-api/internal/upstream"
	"github.com/never/zero-api/internal/store"
)

type ProxyHandler struct {
	channelRepo       *store.ChannelRepo
	modelRepo         *store.ModelRepo
	usageRepo         *store.UsageRepo
	apiKeyRepo        *store.APIKeyRepo
	proxyConfigRepo   *store.ProxyConfigRepo
	breaker           *CircuitBreaker
	proxyConfigCache  *store.ProxyConfigData
	modelsCache       []byte          // /v1/models 响应缓存
	modelsCacheTime   time.Time       // 缓存时间
	modelsCacheMu     sync.RWMutex
	modelsCacheTTL    time.Duration   // 缓存有效期
}

func NewProxyHandler(channelRepo *store.ChannelRepo, modelRepo *store.ModelRepo, usageRepo *store.UsageRepo, apiKeyRepo *store.APIKeyRepo, proxyConfigRepo *store.ProxyConfigRepo) *ProxyHandler {
	h := &ProxyHandler{
		channelRepo:     channelRepo,
		modelRepo:       modelRepo,
		usageRepo:       usageRepo,
		apiKeyRepo:      apiKeyRepo,
		proxyConfigRepo: proxyConfigRepo,
		breaker:         NewCircuitBreaker(),
		modelsCacheTTL:  60 * time.Second,
	}
	cfg, err := proxyConfigRepo.Get()
	if err == nil {
		h.proxyConfigCache = cfg
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
	if h.proxyConfigCache != nil {
		return h.proxyConfigCache
	}
	cfg, err := h.proxyConfigRepo.Get()
	if err != nil {
		return &store.ProxyConfigData{RequestTimeoutSeconds: 60, FailoverEnabled: true}
	}
	h.proxyConfigCache = cfg
	return cfg
}

// ListLocalModels 返回本地启用的模型列表（兼容 OpenAI /v1/models）
// 格式参考 OpenRouter /api/v1/models，返回丰富的模型元信息
// 使用缓存避免频繁 JSON 编码
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
	for _, m := range models {
		if m.Status != "active" {
			continue
		}

		displayName := m.DisplayName
		if displayName == "" {
			displayName = m.ModelID
		}

		// 构建输入/输出模态
		inputModalities := []string{"text"}
		outputModalities := []string{"text"}
		if m.SupportsVision {
			inputModalities = append(inputModalities, "image")
		}

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
			"temperature":          nil,
			"top_p":                nil,
			"top_k":                nil,
			"frequency_penalty":    nil,
			"presence_penalty":     nil,
			"repetition_penalty":   nil,
		}

		entry := gin.H{
			"id":              m.ModelID,
			"name":            displayName,
			"created":         m.CreatedAt.Unix(),
			"description":     fmt.Sprintf("zero-api model: %s via %s", m.ModelID, m.ChannelName),
			"context_length":  m.ContextWindow,
			"architecture": gin.H{
				"modality":          "text->text",
				"input_modalities":  inputModalities,
				"output_modalities": outputModalities,
				"tokenizer":         "Custom",
				"instruct_type":     nil,
			},
			"pricing":             pricing,
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

		// reasoning 字段（OpenRouter 格式）
		if m.SupportsThinking {
			entry["reasoning"] = gin.H{
				"mandatory":       false,
				"default_enabled": true,
			}
		}

		data = append(data, entry)
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

// ChatCompletion 处理聊天补全请求（核心中转）
func (h *ProxyHandler) ChatCompletion(c *gin.Context) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取请求体失败"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	var reqBody struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(bodyBytes, &reqBody); err != nil || reqBody.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 model 字段"})
		return
	}

	// 验证并解析 API Key
	apiKeyID, err := h.resolveAndValidateAPIKey(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
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
	for _, matchedModel := range candidates {
		// 获取渠道
		ch, err := h.channelRepo.GetByID(matchedModel.ChannelID)
		if err != nil {
			lastErr = fmt.Errorf("渠道 %d 获取失败: %w", matchedModel.ChannelID, err)
			log.Printf("[中转] %s", lastErr)
			h.breaker.RecordFailure(matchedModel.ChannelID)
			continue
		}
		if ch.Status != "active" {
			continue
		}

		// 熔断检查（全局开关 + 渠道开关）
		if proxyConfig.FailoverEnabled && ch.FailoverEnabled {
			allow, needProbe := h.breaker.MayProceed(ch.ID)
			if !allow && !needProbe {
				log.Printf("[中转] 渠道 %s (%d) 熔断冷却中，跳过", ch.Name, ch.ID)
				breakerSkipped = true
				continue
			}
			if needProbe {
				log.Printf("[中转] 渠道 %s (%d) 需探测健康状态", ch.Name, ch.ID)
				h.breaker.EnterProbing(ch.ID)
				testModel := ch.TestModel
				if testModel == "" {
					testModel = matchedModel.ModelID
				}
				forwardURL, forwardUser, forwardPass := "", "", ""
				if ch.UseProxy && proxyConfig.ForwardProxyURL != "" {
					forwardURL = proxyConfig.ForwardProxyURL
					forwardUser = proxyConfig.ForwardProxyUser
					forwardPass = proxyConfig.ForwardProxyPass
				}
				if err := h.breaker.ProbeAndRecover(ch.ID, ch, testModel, proxyConfig.ProbeAPIKey, forwardURL, forwardUser, forwardPass, requestTimeout); err != nil {
					log.Printf("[中转] 渠道 %s (%d) 探测失败，跳过: %v", ch.Name, ch.ID, err)
					continue
				}
				// 探测通过，继续执行 tryForward
			}
		}

		if err := h.tryForward(c, bodyBytes, matchedModel, ch, apiKeyID, requestTimeout, proxyConfig, reqBody.Stream); err == nil {
			// 成功，清除熔断状态
			h.breaker.RecordSuccess(ch.ID)
			return
		} else {
			lastErr = err
			h.breaker.RecordFailure(ch.ID)
			log.Printf("[中转] 模型 %s 渠道 %s 失败，尝试下一渠道: %v", reqBody.Model, ch.Name, err)
		}
	}

	// 如果所有候选渠道都因熔断冷却被跳过（没有任何渠道被实际尝试），
	// 说明当前没有可用的渠道，需要清除熔断状态让后续请求重新尝试
	if breakerSkipped && lastErr == nil {
		for _, matchedModel := range candidates {
			ch, err := h.channelRepo.GetByID(matchedModel.ChannelID)
			if err == nil && ch.Status == "active" && h.breaker != nil {
				allow, _ := h.breaker.MayProceed(ch.ID)
				if !allow {
					h.breaker.RecordSuccess(ch.ID)
					log.Printf("[中转] 模型 %s 的渠道 %s (%d) 已清除熔断，等待下次请求重试", reqBody.Model, ch.Name, ch.ID)
				}
			}
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": fmt.Sprintf("模型 %s 的所有候选渠道均暂时不可用（熔断冷却），已重置熔断状态，请重试", reqBody.Model)})
		return
	}

	c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("所有渠道均失败: %v", lastErr)})
}

// InvalidateProxyConfig 清除代理配置缓存和模型列表缓存（设置页面保存后调用）
func (h *ProxyHandler) InvalidateProxyConfig() {
	h.proxyConfigCache = nil
	h.InvalidateModelsCache()
}

// resolveAndValidateAPIKey 从请求中提取并验证 API Key
// 返回 apiKeyID，如果验证失败则返回 error
func (h *ProxyHandler) resolveAndValidateAPIKey(c *gin.Context) (*int64, error) {
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

	return &k.ID, nil
}

func (h *ProxyHandler) recordUsage(requestModel string, rawResp, convertedResp []byte, adapt adapter.Adapter, model *store.Model, channelID int64, apiKeyID *int64, latencyMs int) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Usage] 记录用量 panic 恢复: %v", r)
		}
	}()

	modelID := model.ID
	var promptTokens, completionTokens, cacheHitTokens, totalTokens int
	var cost float64

	// 从响应中提取用量（优先从转换后的响应提取）
	usage, err := adapt.ExtractUsage(convertedResp)
	if err != nil {
		usage, err = adapt.ExtractUsage(rawResp)
	}
	if err != nil {
		log.Printf("[Usage] ExtractUsage 失败 (model=%s): %v — 仍记录请求", requestModel, err)
	} else {
		promptTokens = usage.PromptTokens
		completionTokens = usage.CompletionTokens
		cacheHitTokens = usage.CacheHitTokens
		totalTokens = usage.TotalTokens
		cacheMissTokens := usage.PromptTokens - usage.CacheHitTokens
		cost = (float64(cacheMissTokens)/1000000)*model.PricingInput +
			(float64(usage.CacheHitTokens)/1000000)*model.PricingCacheRead +
			(float64(usage.CompletionTokens)/1000000)*model.PricingOutput
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
		Cost:             cost,
	}); err != nil {
		log.Printf("[Usage] 插入记录失败: %v", err)
	}
}

// tryForward 尝试将请求转发到指定渠道，成功返回 nil，失败返回 error
func (h *ProxyHandler) tryForward(c *gin.Context, bodyBytes []byte, matchedModel *store.Model, ch *store.Channel, apiKeyID *int64, requestTimeout time.Duration, proxyConfig *store.ProxyConfigData, isStream bool) error {
	// 根据渠道类型选择适配器
	adapt := adapter.NewAdapter(ch.Type)

	// 转换请求体（如果需要）
	convertedBody, err := adapt.ConvertRequest(matchedModel.ModelID, bodyBytes)
	if err != nil {
		return fmt.Errorf("请求格式转换失败: %w", err)
	}

	// 构造上游请求
	upstreamURL := adapt.GetChatURL(ch.BaseURL)
	if ch.Type == "gemini" {
		upstreamURL = fmt.Sprintf("%s/%s:generateContent", upstreamURL, matchedModel.ModelID)
		if ch.APIKey != "" {
			upstreamURL = fmt.Sprintf("%s?key=%s", upstreamURL, ch.APIKey)
		}
	}

	// 创建 HTTP 客户端
	// 流式请求使用无总超时的客户端（仅空闲超时），非流式请求使用有总超时的客户端
	startTime := time.Now()

	var upstreamCtx context.Context
	if isStream {
		upstreamCtx = context.Background()
	} else {
		var cancel context.CancelFunc
		upstreamCtx, cancel = context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(upstreamCtx, "POST", upstreamURL, bytes.NewReader(convertedBody))
	if err != nil {
		return fmt.Errorf("构造上游请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if ch.Type == "anthropic" {
		if ch.APIKey != "" {
			req.Header.Set("x-api-key", ch.APIKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else if auth := c.GetHeader("x-api-key"); auth != "" {
			req.Header.Set("x-api-key", auth)
			req.Header.Set("anthropic-version", "2023-06-01")
		}
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
			client, err = upstream.NewStreamHTTPClientWithProxy(
				proxyConfig.ForwardProxyURL,
				proxyConfig.ForwardProxyUser,
				proxyConfig.ForwardProxyPass,
			)
		} else {
			client, err = upstream.NewHTTPClientWithProxyAndTimeout(
				proxyConfig.ForwardProxyURL,
				proxyConfig.ForwardProxyUser,
				proxyConfig.ForwardProxyPass,
				requestTimeout,
			)
		}
		if err != nil {
			log.Printf("[中转] 渠道 %s 代理配置错误，回退直连: %v", ch.Name, err)
		}
	}
	if client == nil {
		if isStream {
			client = upstream.NewStreamHTTPClient()
		} else {
			client = upstream.NewHTTPClientWithTimeout(requestTimeout)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("上游请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 检查可切换错误状态
	if upstream.ShouldFailoverStatus(resp.StatusCode) {
		respBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("上游返回可切换错误状态 %d: %s", resp.StatusCode, string(respBytes))
	}

	if isStream {
		// 流式转发
		err = h.streamResponse(c, resp, adapt, matchedModel, ch, apiKeyID, startTime, requestTimeout)
		if err != nil {
			log.Printf("[流式] 模型 %s 渠道 %s 流式转发错误: %v", matchedModel.ModelID, ch.Name, err)
			// 如果已经向客户端写入任何数据（响应头或 body），
			// 禁止 failover 切换到下一渠道，直接返回 nil
			if c.Writer.Written() {
				log.Printf("[流式] 已在渠道 %s 输出数据，跳过 failover", ch.Name)
				return nil
			}
			return fmt.Errorf("流式转发失败: %w", err)
		}
		return nil
	}

	// === 非流式响应（原逻辑）===
	latencyMs := int(time.Since(startTime).Milliseconds())
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取上游响应失败: %w", err)
	}

	// 转换响应
	convertedResp, err := adapt.ConvertResponse(respBytes)
	if err != nil {
		convertedResp = respBytes
	}

	// 记录使用信息
	go h.recordUsage(matchedModel.ModelID, respBytes, convertedResp, adapt, matchedModel, ch.ID, apiKeyID, latencyMs)

	// 返回响应（过滤逐跳头）
	filteredHeaders := filterHopByHop(resp.Header)
	for k, vals := range filteredHeaders {
		for _, v := range vals {
			c.Header(k, v)
		}
	}
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), convertedResp)
	return nil
}

// streamResponse 流式转发上游 SSE 响应
func (h *ProxyHandler) streamResponse(c *gin.Context, resp *http.Response, adapt adapter.Adapter, matchedModel *store.Model, ch *store.Channel, apiKeyID *int64, startTime time.Time, requestTimeout time.Duration) error {
	// 设置响应头（过滤逐跳头）
	filteredHeaders := filterHopByHop(resp.Header)
	for k, vals := range filteredHeaders {
		for _, v := range vals {
			c.Header(k, v)
		}
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(resp.StatusCode)

	// 清除 http.Server.WriteTimeout（流式响应可能持续很长时间）
	http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})

	// 获取 Flusher
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return fmt.Errorf("ResponseWriter 不支持 Flusher")
	}

	// 流式空闲超时：每次成功读取一行后重置计时器
	// 防止上游中途停滞导致资源泄漏，但不限制长时流式输出
	idleTimer := time.NewTimer(requestTimeout)
	defer idleTimer.Stop()
	idleDone := make(chan struct{})
	defer close(idleDone)
	go func() {
		select {
		case <-idleTimer.C:
			resp.Body.Close()
		case <-idleDone:
		}
	}()

	// 安全重置计时器：处理已过期未 drain 的 case
	safeResetIdle := func() {
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer.Reset(requestTimeout)
	}

	// 使用 bufio.Reader 逐行读取
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	var buf bytes.Buffer

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			// 检查是否客户端断连（不触发熔断）
			if errors.Is(err, io.ErrUnexpectedEOF) || c.Request.Context().Err() != nil {
				log.Printf("[流式] 客户端断开连接 (模型=%s, 渠道=%s)", matchedModel.ModelID, ch.Name)
				return nil
			}
			// 空闲超时或其他连接错误，返回 error 触发熔断回落
			return fmt.Errorf("流式读取失败: %w", err)
		}

		// 成功收到数据，重置空闲计时器
		safeResetIdle()
		buf.Write(line)

		// 写入客户端
		if _, werr := c.Writer.Write(line); werr != nil {
			// 检查是否客户端断连（不触发熔断）
			if c.Request.Context().Err() != nil {
				log.Printf("[流式] 客户端断开连接 (模型=%s, 渠道=%s)", matchedModel.ModelID, ch.Name)
				return nil
			}
			return fmt.Errorf("写入流式响应失败: %w", werr)
		}
		flusher.Flush()

		if err == io.EOF {
			break
		}
	}

	// 记录使用信息
	latencyMs := int(time.Since(startTime).Milliseconds())
	fullRespBytes := buf.Bytes()
	if len(fullRespBytes) > 0 {
		convertedResp, _ := adapt.ConvertResponse(fullRespBytes)
		go h.recordUsage(matchedModel.ModelID, fullRespBytes, convertedResp, adapt, matchedModel, ch.ID, apiKeyID, latencyMs)
	}
	return nil
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
	"transfer-encoding":    true,
	"connection":           true,
	"keep-alive":           true,
	"te":                   true,
	"trailer":              true,
	"upgrade":              true,
	"proxy-authorization":  true,
	"proxy-authenticate":   true,
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
