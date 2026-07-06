package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/never/zero-api/internal/adapter"
	"github.com/never/zero-api/internal/store"
	"github.com/never/zero-api/internal/upstream"
)

// ModelMappingConfig 模型映射配置（运行时用）
type ModelMappingConfig struct {
	TargetModel     string `json:"target_model"`
	Name            string `json:"name"`
	ContextWindow   int    `json:"context_window"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Thinking        bool   `json:"thinking"`
	ReasoningEffort string `json:"reasoning_effort"`
	Vision          bool   `json:"vision"`
}

// ProxyAdapter 代理适配器，处理拦截后的请求转发
type ProxyAdapter struct {
	channelRepo   *store.ChannelRepo
	modelRepo     *store.ModelRepo
	usageRepo     *store.UsageRepo
	apiKeyRepo    *store.APIKeyRepo
	modelMappings map[string]ModelMappingConfig
	requestTimeout time.Duration
}

func NewProxyAdapter(channelRepo *store.ChannelRepo, modelRepo *store.ModelRepo, usageRepo *store.UsageRepo, apiKeyRepo *store.APIKeyRepo, requestTimeout time.Duration) *ProxyAdapter {
	return &ProxyAdapter{
		channelRepo:   channelRepo,
		modelRepo:     modelRepo,
		usageRepo:     usageRepo,
		apiKeyRepo:    apiKeyRepo,
		modelMappings: make(map[string]ModelMappingConfig),
		requestTimeout: requestTimeout,
	}
}

// SetModelMappings 设置模型映射（支持热更新）
func (pa *ProxyAdapter) SetModelMappings(mappings map[string]ModelMappingConfig) {
	pa.modelMappings = mappings
}

// HandleModelsRequest 处理模型列表请求
// 精确匹配 ModelProxy 返回的 OpenRouter 格式（Copilot 依赖此格式判断模型能力）
func (pa *ProxyAdapter) HandleModelsRequest(headers map[string]string) (statusCode int, respHeaders map[string]string, respBody []byte, err error) {
	// 验证 API Key
	if _, err := pa.resolveAndValidateAPIKey(headers); err != nil {
		return 401, nil, nil, fmt.Errorf("API Key 验证失败: %w", err)
	}

	allModels, err := pa.modelRepo.List(0)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("查询模型失败: %w", err)
	}

	now := time.Now().Unix()

	// ★ 精确匹配 ModelProxy 的模型条目结构（openai-adapter.js handleModels）
	type architecture struct {
		Modality        string   `json:"modality"`
		InputModalities []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
		Tokenizer       string   `json:"tokenizer"`
		InstructType    *string  `json:"instruct_type"`
	}

	type topProvider struct {
		ContextLength       int  `json:"context_length"`
		MaxCompletionTokens int  `json:"max_completion_tokens"`
		IsModerated         bool `json:"is_moderated"`
	}

	type modelEntry struct {
		ID                 string            `json:"id"`
		Name               string            `json:"name"`
		Created            int64             `json:"created"`
		Description        string            `json:"description"`
		ContextLength      int               `json:"context_length"`
		Architecture       architecture      `json:"architecture"`
		Pricing            map[string]string `json:"pricing"`
		TopProvider        topProvider       `json:"top_provider"`
		PerRequestLimits   *string           `json:"per_request_limits"`
		SupportedParameters []string         `json:"supported_parameters"`
		DefaultParameters  map[string]interface{} `json:"default_parameters"`
		SupportedVoices    *string           `json:"supported_voices"`
		KnowledgeCutoff    *string           `json:"knowledge_cutoff"`
		ExpirationDate     *string           `json:"expiration_date"`
	}

	var data []modelEntry
	for _, m := range allModels {
		if m.Status != "active" {
			continue
		}
		displayName := m.DisplayName
		if displayName == "" {
			displayName = m.ModelID
		}

		// ★ 构建 architecture（匹配 ModelProxy）
		modality := "text->text"
		inputMods := []string{"text"}
		outputMods := []string{"text"}
		if m.SupportsVision {
			modality = "text+image->text"
			inputMods = []string{"text", "image"}
		}

		// ★ 构建 supported_parameters（匹配 ModelProxy）
		params := []string{
			"max_tokens", "temperature", "top_p", "stop",
			"frequency_penalty", "presence_penalty",
			"tool_choice", "tools", "top_k",
		}
		if m.SupportsThinking {
			params = append(params, "reasoning", "include_reasoning")
		}

		entry := modelEntry{
			ID:            m.ModelID,
			Name:          displayName,
			Created:       now,
			Description:   fmt.Sprintf("zero-api model: %s via %s", m.ModelID, m.ChannelName),
			ContextLength: m.ContextWindow,
			Architecture: architecture{
				Modality:         modality,
				InputModalities:  inputMods,
				OutputModalities: outputMods,
				Tokenizer:        "Custom",
				InstructType:     nil,
			},
			Pricing: func() map[string]string {
				p := map[string]string{
					"prompt":           fmt.Sprintf("%.9f", m.PricingInput/1000000),
					"completion":       fmt.Sprintf("%.9f", m.PricingOutput/1000000),
				}
				if m.PricingCacheRead > 0 {
					p["input_cache_read"] = fmt.Sprintf("%.9f", m.PricingCacheRead/1000000)
				}
				if m.PricingCacheWrite > 0 {
					p["input_cache_write"] = fmt.Sprintf("%.9f", m.PricingCacheWrite/1000000)
				}
				return p
			}(),
			TopProvider: topProvider{
				ContextLength:       m.ContextWindow,
				MaxCompletionTokens: m.MaxOutputTokens,
				IsModerated:         false,
			},
			PerRequestLimits:    nil,
			SupportedParameters: params,
			DefaultParameters: map[string]interface{}{
				"temperature":        nil,
				"top_p":              nil,
				"top_k":              nil,
				"frequency_penalty":  nil,
				"presence_penalty":   nil,
				"repetition_penalty": nil,
			},
			SupportedVoices:     nil,
			KnowledgeCutoff:     nil,
			ExpirationDate:      nil,
		}
		data = append(data, entry)
	}
	if data == nil {
		data = []modelEntry{}
	}

	respBytes, _ := json.Marshal(map[string]interface{}{
		"data": data,
	})

	respHeaders = make(map[string]string)
	respHeaders["Content-Type"] = "application/json"
	respHeaders["Access-Control-Allow-Origin"] = "*"
	respHeaders["Connection"] = "close"

	return 200, respHeaders, respBytes, nil
}

// HandleLLMRequest 处理拦截到的 LLM 请求
func (pa *ProxyAdapter) HandleLLMRequest(method, path string, headers map[string]string, body []byte) (statusCode int, respHeaders map[string]string, respBody []byte, err error) {
	// 解析请求体获取模型名
	var reqModel string
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err == nil {
		if m, ok := parsed["model"].(string); ok {
			reqModel = m
		}
	}

	if reqModel == "" {
		return 0, nil, nil, fmt.Errorf("请求中缺少 model 字段")
	}

	originalModel := reqModel

	// 检查模型映射
	mapping, hasMapping := pa.modelMappings[originalModel]
	targetModel := originalModel
	if hasMapping && mapping.TargetModel != "" {
		targetModel = mapping.TargetModel
		log.Printf("[代理] 模型映射: %s → %s", originalModel, targetModel)
	}

	// 验证并解析 API Key
	apiKeyID, err := pa.resolveAndValidateAPIKey(headers)
	if err != nil {
		return 401, nil, nil, fmt.Errorf("API Key 验证失败: %w", err)
	}

	// 查找所有启用的匹配模型（列表已按 c.priority 排序）
	allModels, err := pa.modelRepo.List(0)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("查询模型失败: %w", err)
	}

	var candidates []*store.Model
	for i, m := range allModels {
		if m.ModelID == targetModel && m.Status == "active" {
			candidates = append(candidates, &allModels[i])
		}
	}
	// 如果目标模型名没找到，尝试用原始模型名
	if len(candidates) == 0 && targetModel != originalModel {
		for i, m := range allModels {
			if m.ModelID == originalModel && m.Status == "active" {
				candidates = append(candidates, &allModels[i])
			}
		}
	}
	// 实在找不到，fallback 到任一活跃模型
	if len(candidates) == 0 {
		for i, m := range allModels {
			if m.Status == "active" {
				candidates = append(candidates, &allModels[i])
				break
			}
		}
	}
	if len(candidates) == 0 {
		return 404, nil, nil, fmt.Errorf("模型 %s 未找到或未启用", originalModel)
	}

	// 按优先级依次尝试，失败时自动切换到下一渠道
	var lastErr error
	for _, matchedModel := range candidates {
		ch, err := pa.channelRepo.GetByID(matchedModel.ChannelID)
		if err != nil {
			lastErr = fmt.Errorf("渠道 %d 获取失败: %w", matchedModel.ChannelID, err)
			continue
		}
		if ch.Status != "active" {
			continue
		}

		statusCode, respHeaders, respBody, err := pa.tryForwardModel(headers, body, originalModel, matchedModel, ch, apiKeyID)
		if err == nil {
			return statusCode, respHeaders, respBody, nil
		}
		lastErr = err
		log.Printf("[代理] 模型 %s 渠道 %s 失败，尝试下一渠道: %v", originalModel, ch.Name, err)
	}

	return 502, nil, nil, fmt.Errorf("所有渠道均失败: %v", lastErr)
}

// resolveAndValidateAPIKey 从请求头中提取并验证 API Key
// 返回 apiKeyID，如果验证失败则返回 error
func (pa *ProxyAdapter) resolveAndValidateAPIKey(headers map[string]string) (*int64, error) {
	auth, ok := headers["authorization"]
	if !ok || auth == "" {
		return nil, fmt.Errorf("缺少 Authorization 头，请提供有效的 API Key")
	}

	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return nil, fmt.Errorf("Authorization 格式错误，需 Bearer <api-key>")
	}

	k, err := pa.apiKeyRepo.GetByKey(parts[1])
	if err != nil {
		return nil, fmt.Errorf("无效的 API Key：密钥不存在或已被禁用")
	}

	return &k.ID, nil
}

// tryForwardModel 尝试将请求转发到指定渠道，成功返回响应，失败返回 error
func (pa *ProxyAdapter) tryForwardModel(headers map[string]string, body []byte, originalModel string, matchedModel *store.Model, ch *store.Channel, apiKeyID *int64) (int, map[string]string, []byte, error) {
	// 检查模型映射
	mapping, hasMapping := pa.modelMappings[originalModel]
	targetModel := originalModel
	if hasMapping && mapping.TargetModel != "" {
		targetModel = mapping.TargetModel
	}

	// 选择适配器
	adapt := adapter.NewAdapter(ch.Type)

	// 转换请求体
	convertedBody, err := adapt.ConvertRequest(matchedModel.ModelID, body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("请求格式转换失败: %w", err)
	}

	// 参数注入
	modifiedBody, err := pa.injectParams(convertedBody, targetModel, mapping)
	if err != nil {
		modifiedBody = convertedBody
	}

	// 构造上游请求
	upstreamURL := adapt.GetChatURL(ch.BaseURL)
	if ch.Type == "gemini" {
		upstreamURL = fmt.Sprintf("%s/%s:generateContent", upstreamURL, matchedModel.ModelID)
		if ch.APIKey != "" {
			upstreamURL = fmt.Sprintf("%s?key=%s", upstreamURL, ch.APIKey)
		}
	}

	req, err := http.NewRequest("POST", upstreamURL, bytes.NewReader(modifiedBody))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("构造上游请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// 设置认证头
	switch ch.Type {
	case "anthropic":
		if ch.APIKey != "" {
			req.Header.Set("x-api-key", ch.APIKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else if key, ok := headers["x-api-key"]; ok {
			req.Header.Set("x-api-key", key)
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	case "openai", "openrouter":
		if ch.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+ch.APIKey)
		} else if auth, ok := headers["authorization"]; ok {
			req.Header.Set("Authorization", auth)
		}
	}

	// 转发
	startTime := time.Now()
	client := upstream.NewHTTPClientWithTimeout(pa.requestTimeout)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("上游请求失败: %w", err)
	}
	defer resp.Body.Close()

	latencyMs := int(time.Since(startTime).Milliseconds())
	respBytes, _ := io.ReadAll(resp.Body)

	// 转换响应
	convertedResp, err := adapt.ConvertResponse(respBytes)
	if err != nil {
		convertedResp = respBytes
	}

	if upstream.ShouldFailoverStatus(resp.StatusCode) {
		return resp.StatusCode, nil, nil, fmt.Errorf("上游返回可切换错误状态 %d: %s", resp.StatusCode, string(respBytes))
	}

	// 重写响应中的模型名
	if hasMapping && originalModel != targetModel {
		rewritten := pa.rewriteResponseModel(convertedResp, targetModel, originalModel)
		if rewritten != nil {
			convertedResp = rewritten
		}
	}

	// 异步记录用量
	go pa.recordUsage(originalModel, respBytes, convertedResp, adapt, matchedModel, ch.ID, apiKeyID, latencyMs)

	// 构建响应头
	respHeaders := make(map[string]string)
	for k := range resp.Header {
		v := resp.Header.Get(k)
		if strings.ToLower(k) == "transfer-encoding" {
			continue
		}
		respHeaders[k] = v
	}

	// 添加上下文窗口头
	if hasMapping && mapping.ContextWindow > 0 {
		cw := fmt.Sprintf("%d", mapping.ContextWindow)
		respHeaders["x-llm-context-window"] = cw
		respHeaders["x-model-context-window"] = cw
		respHeaders["x-max-tokens"] = cw
	}

	return resp.StatusCode, respHeaders, convertedResp, nil
}

// injectParams 注入模型映射参数（model 替换 + thinking/reasoning_effort）
func (pa *ProxyAdapter) injectParams(body []byte, targetModel string, mapping ModelMappingConfig) ([]byte, error) {
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}

	// 替换 model 字段（仅当目标模型名不为空时）
	if targetModel != "" {
		parsed["model"] = targetModel
	}

	// 注入 thinking 参数（如果有映射配置且开启了 thinking）
	if mapping.Thinking {
		// 仅当请求中未显式设置时注入
		if _, exists := parsed["thinking"]; !exists {
			parsed["thinking"] = map[string]interface{}{
				"type": "enabled",
			}
		}

		// 注入 reasoning_effort
		if mapping.ReasoningEffort != "" {
			if _, exists := parsed["reasoning_effort"]; !exists {
				effort := mapping.ReasoningEffort
				// 将 low/medium 映射为 high（DeepSeek 行为）
				if effort == "low" || effort == "medium" {
					effort = "high"
				}
				parsed["reasoning_effort"] = effort
			}
		}
	}

	modified, err := json.Marshal(parsed)
	if err != nil {
		return nil, err
	}
	return modified, nil
}

// rewriteResponseModel 重写响应中的模型名
func (pa *ProxyAdapter) rewriteResponseModel(body []byte, fromModel, toModel string) []byte {
	// 检测是否为 SSE 流式响应（多行 data: {...}）
	bodyStr := string(body)
	if strings.Contains(bodyStr, "data: ") {
		return pa.rewriteSSEResponse(bodyStr, fromModel, toModel)
	}
	// 非流式响应
	return pa.rewriteJSONResponse(body, fromModel, toModel)
}

// rewriteJSONResponse 重写非流式 JSON 响应中的 model 字段
func (pa *ProxyAdapter) rewriteJSONResponse(body []byte, fromModel, toModel string) []byte {
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	if model, ok := parsed["model"].(string); ok && model == fromModel {
		parsed["model"] = toModel
		rewritten, err := json.Marshal(parsed)
		if err != nil {
			return nil
		}
		return rewritten
	}
	return nil
}

// rewriteSSEResponse 重写 SSE 流式响应中的 model 字段
func (pa *ProxyAdapter) rewriteSSEResponse(bodyStr, fromModel, toModel string) []byte {
	lines := strings.Split(bodyStr, "\n")
	rewritten := false
	for i, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if model, ok := chunk["model"].(string); ok && model == fromModel {
			chunk["model"] = toModel
			rewrittenLine, _ := json.Marshal(chunk)
			lines[i] = "data: " + string(rewrittenLine)
			rewritten = true
		}
	}
	if rewritten {
		return []byte(strings.Join(lines, "\n"))
	}
	return nil
}

func (pa *ProxyAdapter) recordUsage(requestModel string, rawResp, convertedResp []byte, adapt adapter.Adapter, model *store.Model, channelID int64, apiKeyID *int64, latencyMs int) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[代理][Usage] 记录用量 panic 恢复: %v", r)
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
		log.Printf("[代理][Usage] ExtractUsage 失败 (model=%s): %v — 仍记录请求", requestModel, err)
	} else {
		promptTokens = usage.PromptTokens
		completionTokens = usage.CompletionTokens
		cacheHitTokens = usage.CacheHitTokens
		totalTokens = usage.TotalTokens

		// 计算费用：prompt_tokens 已包含 cache_hit_tokens，需减去缓存部分再分别计价
		cacheMissTokens := promptTokens - cacheHitTokens
		cost = (float64(cacheMissTokens)/1000000)*model.PricingInput +
			(float64(cacheHitTokens)/1000000)*model.PricingCacheRead +
			(float64(completionTokens)/1000000)*model.PricingOutput
	}

	if _, err := pa.usageRepo.Insert(&store.UsageRecord{
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
		log.Printf("[代理][Usage] 插入记录失败: %v", err)
	}
}

// HandleLLMStreamRequest 处理流式 LLM 请求，SSE 数据直接写入 conn
func (pa *ProxyAdapter) HandleLLMStreamRequest(headers map[string]string, body []byte, conn net.Conn) error {
	// 解析请求体获取模型名
	var reqModel string
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err == nil {
		if m, ok := parsed["model"].(string); ok {
			reqModel = m
		}
	}
	if reqModel == "" {
		writeJSONError(conn, 400, "请求中缺少 model 字段")
		return fmt.Errorf("请求中缺少 model 字段")
	}

	originalModel := reqModel

	// 检查模型映射
	mapping, hasMapping := pa.modelMappings[originalModel]
	targetModel := originalModel
	if hasMapping && mapping.TargetModel != "" {
		targetModel = mapping.TargetModel
		log.Printf("[代理] 模型映射: %s → %s", originalModel, targetModel)
	}

	// 验证并解析 API Key
	apiKeyID, err := pa.resolveAndValidateAPIKey(headers)
	if err != nil {
		writeJSONError(conn, 401, "API Key 验证失败: "+err.Error())
		return fmt.Errorf("API Key 验证失败: %w", err)
	}

	// 查找所有启用的匹配模型
	allModels, err := pa.modelRepo.List(0)
	if err != nil {
		writeJSONError(conn, 502, "查询模型失败")
		return fmt.Errorf("查询模型失败: %w", err)
	}

	var candidates []*store.Model
	for i, m := range allModels {
		if m.ModelID == targetModel && m.Status == "active" {
			candidates = append(candidates, &allModels[i])
		}
	}
	if len(candidates) == 0 && targetModel != originalModel {
		for i, m := range allModels {
			if m.ModelID == originalModel && m.Status == "active" {
				candidates = append(candidates, &allModels[i])
			}
		}
	}
	if len(candidates) == 0 {
		for i, m := range allModels {
			if m.Status == "active" {
				candidates = append(candidates, &allModels[i])
				break
			}
		}
	}
	if len(candidates) == 0 {
		writeJSONError(conn, 404, fmt.Sprintf("模型 %s 未找到或未启用", originalModel))
		return fmt.Errorf("模型 %s 未找到或未启用", originalModel)
	}

	// 按优先级依次尝试，失败时自动切换到下一渠道
	// 注意：一旦 tryForwardModelStream 向连接写入任何数据（SSE 响应头），
	// 后续任何失败都不能再切换到下一渠道，否则会污染已发送的响应。
	var lastErr error
	var dataWritten bool
	for _, matchedModel := range candidates {
		ch, err := pa.channelRepo.GetByID(matchedModel.ChannelID)
		if err != nil {
			lastErr = fmt.Errorf("渠道 %d 获取失败: %w", matchedModel.ChannelID, err)
			continue
		}
		if ch.Status != "active" {
			continue
		}

		// 如果之前已有渠道开始写数据但失败了，禁止 failover
		if dataWritten {
			log.Printf("[代理] 模型 %s 渠道 %s 已在之前输出数据，跳过剩余渠道", originalModel, ch.Name)
			break
		}

		wroteData, err := pa.tryForwardModelStream(conn, headers, body, originalModel, matchedModel, ch, apiKeyID)
		if err == nil {
			return nil
		}
		if wroteData {
			dataWritten = true
		}
		lastErr = err
		log.Printf("[代理] 模型 %s 渠道 %s 流式失败，尝试下一渠道: %v", originalModel, ch.Name, err)
	}

	if !dataWritten {
		writeJSONError(conn, 502, fmt.Sprintf("所有渠道均失败: %v", lastErr))
	}
	return lastErr
}

// tryForwardModelStream 流式转发到指定渠道，SSE 数据直接写入 conn
// 返回 (wroteData, error)
//   wroteData=true 表示已向连接写入数据（此时调用方禁止 failover）
func (pa *ProxyAdapter) tryForwardModelStream(conn net.Conn, headers map[string]string, body []byte, originalModel string, matchedModel *store.Model, ch *store.Channel, apiKeyID *int64) (bool, error) {
	// 检查模型映射
	mapping, hasMapping := pa.modelMappings[originalModel]
	targetModel := originalModel
	if hasMapping && mapping.TargetModel != "" {
		targetModel = mapping.TargetModel
	}

	// 选择适配器
	adapt := adapter.NewAdapter(ch.Type)

	// 转换请求体
	convertedBody, err := adapt.ConvertRequest(matchedModel.ModelID, body)
	if err != nil {
		return false, fmt.Errorf("请求格式转换失败: %w", err)
	}

	// 参数注入
	modifiedBody, err := pa.injectParams(convertedBody, targetModel, mapping)
	if err != nil {
		modifiedBody = convertedBody
	}

	// 构造上游请求
	upstreamURL := adapt.GetChatURL(ch.BaseURL)
	if ch.Type == "gemini" {
		upstreamURL = fmt.Sprintf("%s/%s:generateContent", upstreamURL, matchedModel.ModelID)
		if ch.APIKey != "" {
			upstreamURL = fmt.Sprintf("%s?key=%s", upstreamURL, ch.APIKey)
		}
	}

	req, err := http.NewRequest("POST", upstreamURL, bytes.NewReader(modifiedBody))
	if err != nil {
		return false, fmt.Errorf("构造上游请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// 设置认证头
	switch ch.Type {
	case "anthropic":
		if ch.APIKey != "" {
			req.Header.Set("x-api-key", ch.APIKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else if key, ok := headers["x-api-key"]; ok {
			req.Header.Set("x-api-key", key)
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	case "openai", "openrouter":
		if ch.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+ch.APIKey)
		} else if auth, ok := headers["authorization"]; ok {
			req.Header.Set("Authorization", auth)
		}
	}

	// 转发
	startTime := time.Now()
	client := upstream.NewStreamHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("上游请求失败: %w", err)
	}
	defer resp.Body.Close()

	if upstream.ShouldFailoverStatus(resp.StatusCode) {
		respBytes, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("上游返回可切换错误状态 %d: %s", resp.StatusCode, string(respBytes))
	}

	// 流式空闲超时：每次成功读取一行后重置计时器
	// 防止上游停止推流后资源泄漏，但不限制正常的长时流式输出
	idleTimer := time.NewTimer(pa.requestTimeout)
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

	// 安全重置计时器
	safeResetIdle := func() {
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer.Reset(pa.requestTimeout)
	}

	// 构造 SSE 响应头（写回原始 TLS 连接）
	var headerBuf strings.Builder
	headerBuf.WriteString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode)))
	headerBuf.WriteString("Content-Type: text/event-stream\r\n")
	headerBuf.WriteString("Cache-Control: no-cache\r\n")
	headerBuf.WriteString("Connection: close\r\n")
	// 透传原始上游响应头（过滤掉逐跳头和已设置的头）
	for k := range resp.Header {
		kl := strings.ToLower(k)
		if kl == "transfer-encoding" || kl == "content-length" || kl == "connection" ||
			kl == "content-type" || kl == "cache-control" {
			continue
		}
		headerBuf.WriteString(fmt.Sprintf("%s: %s\r\n", k, resp.Header.Get(k)))
	}
	headerBuf.WriteString("\r\n")

	if _, err := conn.Write([]byte(headerBuf.String())); err != nil {
		return true, fmt.Errorf("写入 SSE 响应头失败: %w", err)
	}

	// 流式转发 SSE 数据块
	var buf bytes.Buffer
	streamReader := bufio.NewReaderSize(resp.Body, 64*1024)
	done := false
	for !done {
		line, err := streamReader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				done = true
				if len(line) == 0 {
					break
				}
			} else {
				return true, fmt.Errorf("读取 SSE 流失败: %w", err)
			}
		}
		if _, werr := conn.Write(line); werr != nil {
			// 检查客户端是否断连：尝试短读 1 字节
			conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			if n, readErr := conn.Read(make([]byte, 1)); n == 0 && readErr != nil {
				// 客户端已断开，正常返回而非错误
				return true, nil
			}
			conn.SetReadDeadline(time.Time{})
			return true, fmt.Errorf("写入 SSE 数据失败: %w", werr)
		}
		// 成功写入数据，重置空闲计时器
		safeResetIdle()
		buf.Write(line)
	}

	// 异步记录用量
	latencyMs := int(time.Since(startTime).Milliseconds())
	fullResp := buf.Bytes()
	if len(fullResp) > 0 {
		convertedResp, _ := adapt.ConvertResponse(fullResp)
		go pa.recordUsage(originalModel, fullResp, convertedResp, adapt, matchedModel, ch.ID, apiKeyID, latencyMs)
	}

	return false, nil
}

// writeJSONError 向原始连接写入 JSON 错误响应
func writeJSONError(conn net.Conn, statusCode int, msg string) {
	body, _ := json.Marshal(map[string]string{"error": msg})
	writeHTTPResponseWithHeaders(conn, statusCode, map[string]string{
		"Content-Type": "application/json",
		"Connection":   "close",
	}, body)
}
