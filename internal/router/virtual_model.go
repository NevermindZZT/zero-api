package router

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/never/zero-api/internal/adapter"
	"github.com/never/zero-api/internal/pricing"
	"github.com/never/zero-api/internal/store"
	"github.com/never/zero-api/internal/upstream"
)

// VirtualModelRouter 虚拟模型路由规则：
// 下游请求虚拟模型名时：
//   - 无图请求：直接替换模型名为主模型（零额外成本）
//   - 有图请求：先调识图模型识别图片 → 图片原位替换为文字描述 → 主模型继续回答
//
// 支持 openai / anthropic / responses 三种下游协议（协议感知的图片提取/替换）。
type VirtualModelRouter struct {
	virtualModelRepo *store.VirtualModelRepo
	modelRepo        *store.ModelRepo
	channelRepo      *store.ChannelRepo
	usageRepo        *store.UsageRepo
	apiKeyRepo       *store.APIKeyRepo
	proxyConfigFn    func() *store.ProxyConfigData
	// downloadTimeout 图片下载超时（0 = 默认 10s，测试可覆盖）
	downloadTimeout time.Duration
}

// NewVirtualModelRouter 创建虚拟模型路由规则
func NewVirtualModelRouter(virtualModelRepo *store.VirtualModelRepo, modelRepo *store.ModelRepo, channelRepo *store.ChannelRepo, proxyConfigFn func() *store.ProxyConfigData) *VirtualModelRouter {
	return &VirtualModelRouter{
		virtualModelRepo: virtualModelRepo,
		modelRepo:        modelRepo,
		channelRepo:      channelRepo,
		proxyConfigFn:    proxyConfigFn,
	}
}

// SetUsageRepo 设置 usage 存储（识图调用计费记录）
func (r *VirtualModelRouter) SetUsageRepo(repo *store.UsageRepo) {
	r.usageRepo = repo
}

// SetAPIKeyRepo 设置 API Key 存储（识图调用额度扣减）
func (r *VirtualModelRouter) SetAPIKeyRepo(repo *store.APIKeyRepo) {
	r.apiKeyRepo = repo
}

// SetDownloadTimeout 覆盖图片下载超时（测试用）
func (r *VirtualModelRouter) SetDownloadTimeout(d time.Duration) {
	r.downloadTimeout = d
}

// Name 规则名称
func (r *VirtualModelRouter) Name() string { return "virtual-model" }

// Match 命中规则：请求模型名是已存在的虚拟模型
func (r *VirtualModelRouter) Match(ctx *Context) bool {
	if r.virtualModelRepo == nil {
		return false
	}
	vm, err := r.virtualModelRepo.GetByName(ctx.Model)
	return err == nil && vm != nil
}

// Transform 处理虚拟模型请求
func (r *VirtualModelRouter) Transform(ctx *Context) *Result {
	vm, err := r.virtualModelRepo.GetByName(ctx.Model)
	if err != nil || vm == nil {
		return nil
	}
	if vm.Status != "active" {
		return ErrorResult(http.StatusNotFound, "虚拟模型 %s 已禁用", vm.Name)
	}

	// 主模型本身支持视觉且未配置识图扩展时，虚拟模型只是模型别名/路由入口。
	// 直接只替换顶层 model，完全不解析或重建 input，避免破坏 Responses 原生字段
	//（reasoning.summary、previous_response_id、function_call_output 等）。
	if vm.VisionModel == "" && r.mainModelSupportsVision(vm.MainModel) {
		newBody := ReplaceModel(ctx.RawBody, vm.MainModel)
		if newBody == nil {
			return nil
		}
		log.Printf("[路由:虚拟模型] %s 原生视觉直通主模型 %s", vm.Name, vm.MainModel)
		return &Result{NewBody: newBody}
	}

	// 只有配置了识图扩展，或主模型不支持视觉时，才需要解析图片。
	// 协议感知的图片提取：只检测最后一条 user 消息的图片。
	images := ExtractLatestUserImages(ctx.Protocol, ctx.RawBody)
	usable := extractImageRefsToUsable(images)
	log.Printf("[路由:虚拟模型] %s 请求: 最新图片数=%d stream=%v 协议=%s", vm.Name, len(images), ctx.Stream, ctx.Protocol)
	// 调试：打印消息角色序列和各消息图片数（定位 agent 多轮场景的识图触发问题）
	logMessageImageProfile(ctx.Protocol, ctx.RawBody)
	// 请求中所有图片数（含历史）——若大于 0 但未触发识图，说明被"历史图片"规则拦截
	allImages := ExtractImages(ctx.Protocol, ctx.RawBody)
	if len(allImages) > 0 && len(images) == 0 {
		log.Printf("[路由:虚拟模型] ⚠️ %s 请求含 %d 张图片但未触发识图（可能被历史图片规则拦截，最后带图 user 后已有 assistant 回复）", vm.Name, len(allImages))
		logMessageImageProfile(ctx.Protocol, ctx.RawBody)
	}

	// 无图请求：仅替换模型名，必须保留 Responses 原生 input（reasoning.summary、
	// previous_response_id、function_call_output 等）不被重新序列化或裁剪。
	if len(images) == 0 {
		newBody := ReplaceModel(ctx.RawBody, vm.MainModel)
		if newBody == nil {
			return nil // 无法解析，交给普通链路报错
		}
		if os.Getenv("ZERO_API_DEBUG_ROUTER") == "1" {
			log.Printf("[路由:虚拟模型] %s 无图请求仅替换模型名:\n%s", vm.Name, string(newBody))
		}
		return &Result{NewBody: newBody}
	}

	// 有图请求：检查识图模型
	if vm.VisionModel == "" {
		// 未配置识图扩展时，虚拟模型就是主模型的可切换别名。
		// 主模型本身支持视觉则保留原始图片，直接替换模型名透传。
		if r.mainModelSupportsVision(vm.MainModel) {
			newBody := ReplaceModel(ctx.RawBody, vm.MainModel)
			if newBody == nil {
				return nil
			}
			log.Printf("[路由:虚拟模型] %s 使用主模型 %s 原生识图", vm.Name, vm.MainModel)
			return &Result{NewBody: newBody}
		}
		return ErrorResult(http.StatusBadRequest, "虚拟模型 %s 的主模型 %s 不支持视觉，请配置识图模型", vm.Name, vm.MainModel)
	}
	if len(usable) == 0 {
		// 有图片但无可识别引用（如 responses file_id），交给主模型处理：
		// 同样替换所有 user 图片为占位（无可识别引用的图片无法识图，不能透传原始图）
		newBody := ReplaceImages(ctx.Protocol, ctx.RawBody, vm.MainModel, nil)
		if newBody == nil {
			return nil
		}
		return &Result{NewBody: newBody}
	}

	// 流式预响应：先告知下游"正在分析图片"，再开始耗时识图（避免长时间无响应）
	if ctx.Stream && ctx.StreamPreface != nil {
		if err := ctx.StreamPreface(vm.Name, "正在分析图片...\n"); err != nil {
			log.Printf("[路由:虚拟模型] %s 预响应客户端断开: %v", vm.Name, err)
			return ErrorResult(http.StatusBadRequest, "客户端已断开")
		}
	}

	// 阶段1：调用识图模型获取图片描述
	descriptions, err := r.describeImages(vm.VisionModel, usable, ctx.ClientAuth, ctx.APIKeyID)
	if err != nil {
		// 识图失败：注入明确指令——禁止主模型尝试其他手段识图，直接如实告知用户
		log.Printf("[路由:虚拟模型] %s 识图失败: %v", vm.Name, err)
		descriptions = []string{fmt.Sprintf("%s（错误: %v）", VisionFailureInstruction, err)}
	} else {
		// 识图成功：在第一条描述前注入指令——视觉内容已提供，禁止主模型读图/调工具看图
		descriptions[0] = VisionInstruction + "\n" + descriptions[0]
	}

	// 阶段2：图片原位替换为描述文本，模型名替换为主模型
	newBody := ReplaceImages(ctx.Protocol, ctx.RawBody, vm.MainModel, descriptions)
	if newBody == nil {
		return nil
	}

	// 流程确认：打印替换后请求的摘要（图片数/描述长度），环境变量 ZERO_API_DEBUG_ROUTER=1 时打印完整请求体
	logImageReplaceSummary(ctx, vm, len(images), descriptions, newBody)
	if os.Getenv("ZERO_API_DEBUG_ROUTER") == "1" {
		log.Printf("[路由:虚拟模型] %s 替换后请求体:\n%s", vm.Name, string(newBody))
	}
	return &Result{NewBody: newBody}
}

func (r *VirtualModelRouter) mainModelSupportsVision(modelID string) bool {
	if r.modelRepo == nil {
		return false
	}
	supported, err := r.modelRepo.SupportsVisionByModelID(modelID)
	return err == nil && supported
}

// logImageReplaceSummary 打印识图替换摘要（供确认流程正确性）
func logImageReplaceSummary(ctx *Context, vm *store.VirtualModel, imageCount int, descriptions []string, newBody []byte) {
	// 检查替换后请求是否还残留图片
	remain := ExtractImages(ctx.Protocol, newBody)
	descLen := 0
	for _, d := range descriptions {
		descLen += len(d)
	}
	log.Printf("[路由:虚拟模型] %s 识图完成: 图片=%d 描述=%d条(%d字符) 替换后残留图片=%d 主模型=%s",
		vm.Name, imageCount, len(descriptions), descLen, len(remain), vm.MainModel)
	if len(remain) > 0 {
		log.Printf("[路由:虚拟模型] ⚠️ %s 替换后仍残留 %d 张图片（未完全替换）", vm.Name, len(remain))
	}
}

// describeImages 调用识图模型获取图片描述（非流式，独立较短超时）
// 识图请求构造为 OpenAI 兼容格式（content 数组含 image_url），仅路由到 OpenAI 兼容渠道
// clientAuth 为下游透传的 Authorization（渠道未配置 key 时使用）；apiKeyID 用于用量记录/额度扣减
func (r *VirtualModelRouter) describeImages(visionModel string, imageURLs []string, clientAuth string, apiKeyID *int64) ([]string, error) {
	// 预下载 URL 图片转 base64（上游可能无法直接访问外部图片 URL）
	// data URL / base64 图片直接使用，下载失败回退原 URL
	proxyConfig := r.getProxyConfig()
	prepared := make([]string, 0, len(imageURLs))
	for _, u := range imageURLs {
		if strings.HasPrefix(u, "data:") {
			prepared = append(prepared, u)
			continue
		}
		if b64, ok := r.downloadImageToDataURL(u, proxyConfig); ok {
			prepared = append(prepared, b64)
		} else {
			prepared = append(prepared, u) // 下载失败回退原 URL
		}
	}

	// 构造识图请求（OpenAI chat 格式，含图片）
	contentParts := []map[string]interface{}{
		{"type": "text", "text": "请详细描述图片的内容，包括主体、场景、文字、颜色、布局等细节。如果有多个问题或指令，请一并回答。用中文回答。"},
	}
	for _, u := range prepared {
		contentParts = append(contentParts, map[string]interface{}{
			"type":      "image_url",
			"image_url": map[string]interface{}{"url": u},
		})
	}

	body := map[string]interface{}{
		"model": visionModel,
		"messages": []map[string]interface{}{
			{"role": "user", "content": contentParts},
		},
		"max_tokens": 1024,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("构造识图请求失败: %w", err)
	}

	// 查找识图模型的活跃渠道（OpenAI 兼容渠道）
	allModels, err := r.modelRepo.List(0)
	if err != nil {
		return nil, fmt.Errorf("查询模型失败: %w", err)
	}
	var candidates []*store.Model
	for i, m := range allModels {
		if m.ModelID == visionModel && m.Status == "active" {
			candidates = append(candidates, &allModels[i])
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("识图模型 %s 未找到或未启用", visionModel)
	}

	// 识图请求超时：独立配置 vision_timeout_seconds（默认 60s）
	// 注意：不再硬编码 cap 30s——识图模型（如 mimo 系列）推理耗时可达 30-60s，
	// 过短的超时会导致识图被截断、两阶段路由失败
	visionTimeout := time.Duration(proxyConfig.VisionTimeoutSeconds) * time.Second
	if proxyConfig.VisionTimeoutSeconds <= 0 {
		visionTimeout = 60 * time.Second
	}

	var lastErr error
	for _, matchedModel := range candidates {
		ch, err := r.channelRepo.GetByID(matchedModel.ChannelID)
		if err != nil || ch.Status != "active" {
			continue
		}
		// 识图请求走 OpenAI 兼容格式，仅支持 openai 渠道
		if ch.Type != "openai" && ch.Type != "openrouter" {
			continue
		}

		adapt := adapter.NewAdapter(ch.Type)
		upstreamURL := adapt.GetChatURL(ch.BaseURL)

		upstreamCtx, cancel := context.WithTimeout(context.Background(), visionTimeout)
		defer cancel()

		req, err := http.NewRequestWithContext(upstreamCtx, "POST", upstreamURL, bytes.NewReader(bodyBytes))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if ch.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+ch.APIKey)
		} else if clientAuth != "" {
			req.Header.Set("Authorization", clientAuth)
		}

		var client *http.Client
		if ch.UseProxy && proxyConfig.ForwardProxyURL != "" {
			client, err = upstream.NewHTTPClientWithProxyAndTimeout(
				proxyConfig.ForwardProxyURL, proxyConfig.ForwardProxyUser, proxyConfig.ForwardProxyPass, visionTimeout,
			)
			if err != nil {
				client = upstream.NewHTTPClientWithTimeout(visionTimeout)
			}
		} else {
			client = upstream.NewHTTPClientWithTimeout(visionTimeout)
		}

		startTime := time.Now()
		resp, err := client.Do(req)
		latencyMs := int(time.Since(startTime).Milliseconds())
		if err != nil {
			// 请求失败（超时/网络错误）：上游可能已产生费用，记录一条请求（cost=0 兜底）
			log.Printf("[路由:虚拟模型] 识图请求失败 %s: %v", ch.Name, err)
			r.recordVisionUsage(matchedModel, ch.ID, nil, latencyMs, apiKeyID, true)
			lastErr = err
			continue
		}
		respBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("识图 HTTP %d: %s", resp.StatusCode, truncate(string(respBytes), 200))
			// 上游有响应但失败（如 400/5xx）：记录请求（响应可能含 usage，尝试提取）
			r.recordVisionUsage(matchedModel, ch.ID, respBytes, latencyMs, apiKeyID, true)
			continue
		}

		// 解析响应提取文本（兼容 content 字符串 / content 数组 / reasoning 字段）
		text, extractErr := extractVisionText(respBytes)
		if extractErr == nil && text != "" {
			// 记录识图调用的用量（使用统计中能看到识图模型的消耗）
			r.recordVisionUsage(matchedModel, ch.ID, respBytes, latencyMs, apiKeyID, false)
			log.Printf("[路由:虚拟模型] 识图成功: 模型=%s 渠道=%s 耗时=%dms 描述=%d字符",
				visionModel, ch.Name, latencyMs, len(text))
			return []string{text}, nil
		}
		if extractErr != nil {
			lastErr = fmt.Errorf("解析识图响应失败: %v (原始: %s)", extractErr, truncate(string(respBytes), 300))
			continue
		}
		lastErr = fmt.Errorf("识图响应无内容 (原始: %s)", truncate(string(respBytes), 300))
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("识图模型 %s 无可用渠道", visionModel)
}

// recordVisionUsage 记录识图调用的用量（使用统计中可看到识图模型消耗）
// 关联调用方 API Key（apiKeyID），并扣减其额度（与 handler.recordUsage 一致）
// failed=true 表示识图失败（超时/HTTP 错误）：记录一条请求（tokens 尽量提取，提取不到为 0），
// 让使用统计可见识图模型被调用过（上游可能已产生费用）
func (r *VirtualModelRouter) recordVisionUsage(model *store.Model, channelID int64, respBytes []byte, latencyMs int, apiKeyID *int64, failed bool) {
	if r.usageRepo == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[路由:虚拟模型] 识图用量记录 panic: %v", p)
		}
	}()

	// 提取用量（识图响应为 OpenAI 兼容格式；失败时响应可能不存在或为空）
	var promptTokens, completionTokens, cacheHitTokens, totalTokens int
	var cost float64
	if respBytes != nil && len(respBytes) > 0 {
		usage, err := (&adapter.OpenAIAdapter{}).ExtractUsage(respBytes)
		if err == nil {
			promptTokens = usage.PromptTokens
			completionTokens = usage.CompletionTokens
			cacheHitTokens = usage.CacheHitTokens
			totalTokens = usage.TotalTokens

			// 定价计算（与 handler recordUsage 一致）
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
	}

	modelID := model.ID
	requestModel := model.ModelID
	if failed {
		// 失败记录：request_model 加标记便于统计区分
		requestModel = model.ModelID + "__vision_failed"
		log.Printf("[路由:虚拟模型] 识图失败记录 usage: model=%s tokens=%d/%d cost=%.6f latency=%dms",
			model.ModelID, promptTokens, completionTokens, cost, latencyMs)
	}
	if _, err := r.usageRepo.Insert(&store.UsageRecord{
		ChannelID:        &channelID,
		ModelID:          &modelID,
		APIKeyID:         apiKeyID,
		RequestModel:     requestModel,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CacheHitTokens:   cacheHitTokens,
		TotalTokens:      totalTokens,
		LatencyMs:        latencyMs,
		TotalDurationMs:  latencyMs,
		Cost:             cost,
	}); err != nil {
		log.Printf("[路由:虚拟模型] 识图用量记录失败: %v", err)
	}

	// 扣减 API Key 额度（启用额度的 key 按实际费用扣减，与 handler.recordUsage 一致）
	if apiKeyID != nil && cost > 0 && r.apiKeyRepo != nil {
		if _, err := r.apiKeyRepo.DeductQuota(*apiKeyID, cost); err != nil {
			log.Printf("[Quota] 识图调用 API Key %d 扣减额度失败: %v", *apiKeyID, err)
		}
	}
}

// getProxyConfig 获取代理配置（带 nil 保护，供独立使用时兜底）
func (r *VirtualModelRouter) getProxyConfig() *store.ProxyConfigData {
	if r.proxyConfigFn == nil {
		return &store.ProxyConfigData{RequestTimeoutSeconds: 60}
	}
	pc := r.proxyConfigFn()
	if pc == nil {
		return &store.ProxyConfigData{RequestTimeoutSeconds: 60}
	}
	return pc
}

// extractVisionText 从 OpenAI chat 响应中健壮提取文本
// 兼容三种形态：
//   - message.content 字符串（标准 OpenAI）
//   - message.content 数组（[{type:text,text:"..."}]）
//   - message.reasoning / reasoning_content（部分模型把回答放思考字段，如 mimo 系列 content=null 时 reasoning 有内容）
func extractVisionText(respBytes []byte) (string, error) {
	var chatResp struct {
		Choices []struct {
			Message struct {
				Content          json.RawMessage `json:"content"`
				Reasoning        string          `json:"reasoning"`
				ReasoningContent string          `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBytes, &chatResp); err != nil {
		return "", err
	}
	if len(chatResp.Choices) == 0 {
		return "", nil
	}
	msg := chatResp.Choices[0].Message

	// 1. content 字符串
	var str string
	if err := json.Unmarshal(msg.Content, &str); err == nil && str != "" {
		return str, nil
	}
	// 2. content 数组（text 块拼接）
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(msg.Content, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				sb.WriteString(b.Text)
			}
		}
		if sb.Len() > 0 {
			return sb.String(), nil
		}
	}
	// 3. reasoning 字段兜底（mimo 等模型 content=null 时思考内容在 reasoning）
	if msg.Reasoning != "" {
		return msg.Reasoning, nil
	}
	if msg.ReasoningContent != "" {
		return msg.ReasoningContent, nil
	}
	return "", nil
}

// downloadImageToDataURL 下载图片并转为 base64 data URL（供识图请求使用）
// 返回 (dataURL, true) 成功；(原URL, false) 失败（调用方回退原 URL）
// 下载超时 10s、大小限制 5MB；直连下载（不走 forward proxy——代理可能内网不可达，
// 而图片下载是旁路操作，失败会回退原 URL 交由上游处理，不影响主流程）
func (r *VirtualModelRouter) downloadImageToDataURL(url string, proxyConfig *store.ProxyConfigData) (string, bool) {
	_ = proxyConfig // 保留参数签名，直连下载
	dlTimeout := r.downloadTimeout
	if dlTimeout <= 0 {
		dlTimeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), dlTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		log.Printf("[路由:虚拟模型] 图片下载构造请求失败 %s: %v", url, err)
		return url, false
	}
	// 模拟浏览器 UA，部分图片 CDN 校验 UA
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36")
	req.Header.Set("Accept", "image/*,*/*;q=0.8")

	client := upstream.NewHTTPClientWithTimeout(dlTimeout)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[路由:虚拟模型] 图片下载失败 %s: %v", url, err)
		return url, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[路由:虚拟模型] 图片下载非 200 %s: %d", url, resp.StatusCode)
		return url, false
	}

	// 大小限制 5MB
	limited := io.LimitReader(resp.Body, 5*1024*1024+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		log.Printf("[路由:虚拟模型] 图片读取失败 %s: %v", url, err)
		return url, false
	}
	if len(data) > 5*1024*1024 {
		log.Printf("[路由:虚拟模型] 图片过大 %s: %d bytes", url, len(data))
		return url, false
	}

	// 检测 Content-Type
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" || !strings.HasPrefix(contentType, "image/") {
		contentType = "image/jpeg" // 默认
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	log.Printf("[路由:虚拟模型] 图片已下载转 base64: %s (%d bytes, %s)", url, len(data), contentType)
	return fmt.Sprintf("data:%s;base64,%s", contentType, b64), true
}

// truncate 截断字符串（错误信息用）
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
