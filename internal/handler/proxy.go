package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	proxyConfigCache  *store.ProxyConfigData // 缓存 proxy_config，减少重复 DB 读取
}

func NewProxyHandler(channelRepo *store.ChannelRepo, modelRepo *store.ModelRepo, usageRepo *store.UsageRepo, apiKeyRepo *store.APIKeyRepo, proxyConfigRepo *store.ProxyConfigRepo) *ProxyHandler {
	h := &ProxyHandler{
		channelRepo:     channelRepo,
		modelRepo:       modelRepo,
		usageRepo:       usageRepo,
		apiKeyRepo:      apiKeyRepo,
		proxyConfigRepo: proxyConfigRepo,
		breaker:         NewCircuitBreaker(),
	}
	// 预读取 proxyConfig 到缓存
	cfg, err := proxyConfigRepo.Get()
	if err == nil {
		h.proxyConfigCache = cfg
	}
	return h
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
func (h *ProxyHandler) ListLocalModels(c *gin.Context) {
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
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
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
		Model string `json:"model"`
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

		if err := h.tryForward(c, bodyBytes, matchedModel, ch, apiKeyID, requestTimeout, proxyConfig); err == nil {
			// 成功，清除熔断状态
			h.breaker.RecordSuccess(ch.ID)
			return
		} else {
			lastErr = err
			h.breaker.RecordFailure(ch.ID)
			log.Printf("[中转] 模型 %s 渠道 %s 失败，尝试下一渠道: %v", reqBody.Model, ch.Name, err)
		}
	}

	c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("所有渠道均失败: %v", lastErr)})
}

// InvalidateProxyConfig 清除代理配置缓存（设置页面保存后调用）
func (h *ProxyHandler) InvalidateProxyConfig() {
	h.proxyConfigCache = nil
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

func (h *ProxyHandler) recordUsage(reqBody, rawResp, convertedResp []byte, adapt adapter.Adapter, model *store.Model, channelID int64, apiKeyID *int64, latencyMs int) {
	var req struct {
		Model string `json:"model"`
	}
	json.Unmarshal(reqBody, &req)

	modelID := model.ID
	var promptTokens, completionTokens, cacheHitTokens, totalTokens int
	var cost float64

	// 从响应中提取用量（优先从转换后的响应提取）
	usage, err := adapt.ExtractUsage(convertedResp)
	if err != nil {
		usage, err = adapt.ExtractUsage(rawResp)
	}
	if err != nil {
		log.Printf("[Usage] ExtractUsage 失败 (model=%s): %v — 仍记录请求", req.Model, err)
		// 即使提取失败也记录一条空用量，确保请求被计数
	} else {
		promptTokens = usage.PromptTokens
		completionTokens = usage.CompletionTokens
		cacheHitTokens = usage.CacheHitTokens
		totalTokens = usage.TotalTokens
		// 计算费用：prompt_tokens 已包含 cache_hit_tokens，需减去缓存部分再分别计价
		cacheMissTokens := usage.PromptTokens - usage.CacheHitTokens
		cost = (float64(cacheMissTokens)/1000000)*model.PricingInput +
			(float64(usage.CacheHitTokens)/1000000)*model.PricingCacheRead +
			(float64(usage.CompletionTokens)/1000000)*model.PricingOutput
	}

	if _, err := h.usageRepo.Insert(&store.UsageRecord{
		ChannelID:        &channelID,
		ModelID:          &modelID,
		APIKeyID:         apiKeyID,
		RequestModel:     req.Model,
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
func (h *ProxyHandler) tryForward(c *gin.Context, bodyBytes []byte, matchedModel *store.Model, ch *store.Channel, apiKeyID *int64, requestTimeout time.Duration, proxyConfig *store.ProxyConfigData) error {
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

	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", upstreamURL, bytes.NewReader(convertedBody))
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

	// 转发请求
	startTime := time.Now()
	var client *http.Client
	if ch.UseProxy && proxyConfig.ForwardProxyURL != "" {
		client, err = upstream.NewHTTPClientWithProxyAndTimeout(
			proxyConfig.ForwardProxyURL,
			proxyConfig.ForwardProxyUser,
			proxyConfig.ForwardProxyPass,
			requestTimeout,
		)
		if err != nil {
			log.Printf("[中转] 渠道 %s 代理配置错误，回退直连: %v", ch.Name, err)
		}
	}
	if client == nil {
		client = upstream.NewHTTPClientWithTimeout(requestTimeout)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("上游请求失败: %w", err)
	}
	defer resp.Body.Close()

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

	if shouldFailoverStatus(resp.StatusCode) {
		return fmt.Errorf("上游返回可切换错误状态 %d: %s", resp.StatusCode, string(respBytes))
	}

	// 记录使用信息
	go h.recordUsage(bodyBytes, respBytes, convertedResp, adapt, matchedModel, ch.ID, apiKeyID, latencyMs)

	// 返回响应
	for k, vals := range resp.Header {
		for _, v := range vals {
			c.Header(k, v)
		}
	}
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), convertedResp)
	return nil
}

func shouldFailoverStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return statusCode >= 500
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
