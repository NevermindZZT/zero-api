package handler

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/openrouter"
	"github.com/never/zero-api/internal/store"
	"github.com/never/zero-api/internal/upstream"
)

type OpenRouterHandler struct {
	modelRepo *store.ModelRepo
}

func NewOpenRouterHandler(modelRepo *store.ModelRepo) *OpenRouterHandler {
	return &OpenRouterHandler{modelRepo: modelRepo}
}

type openRouterPreviewRequest struct {
	ModelIDs []int64 `json:"model_ids"`
}

type openRouterCandidate struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	ContextLength     int      `json:"context_length"`
	MaxOutputTokens   int      `json:"max_output_tokens"`
	InputModalities   []string `json:"input_modalities"`
	OutputModalities  []string `json:"output_modalities"`
	SupportsVision    bool     `json:"supports_vision"`
	SupportsThinking  bool     `json:"supports_thinking"`
	SupportsTools     bool     `json:"supports_tools"`
	Capabilities      []string `json:"capabilities"`
	PricingInput      float64  `json:"pricing_input"`
	PricingOutput     float64  `json:"pricing_output"`
	PricingCacheRead  float64  `json:"pricing_cache_read"`
	PricingCacheWrite float64  `json:"pricing_cache_write"`
}

type openRouterPreviewItem struct {
	LocalModelID int64                 `json:"local_model_id"`
	ModelID      string                `json:"model_id"`
	Candidates   []openRouterCandidate `json:"candidates"`
	ExactMatchID string                `json:"exact_match_id,omitempty"`
}

func (h *OpenRouterHandler) Preview(c *gin.Context) {
	var req openRouterPreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.ModelIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供要同步的模型 ID"})
		return
	}
	catalog, err := openrouter.Fetch(upstream.NewHTTPClient())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	modelsByID := make(map[string]openrouter.Model, len(catalog))
	for _, model := range catalog {
		modelsByID[model.ID] = model
	}
	allCandidates := make([]openRouterCandidate, 0, len(catalog))
	for _, model := range catalog {
		allCandidates = append(allCandidates, candidateFromOpenRouter(model))
	}
	sort.Slice(allCandidates, func(i, j int) bool { return allCandidates[i].ID < allCandidates[j].ID })

	items := make([]openRouterPreviewItem, 0, len(req.ModelIDs))
	seen := make(map[int64]struct{})
	for _, id := range req.ModelIDs {
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		local, err := h.modelRepo.GetByID(id)
		if err != nil {
			continue
		}
		item := openRouterPreviewItem{LocalModelID: local.ID, ModelID: local.ModelID}
		if _, ok := modelsByID[local.ModelID]; ok {
			item.ExactMatchID = local.ModelID
			item.Candidates = []openRouterCandidate{candidateFromOpenRouter(modelsByID[local.ModelID])}
		} else {
			for _, candidate := range allCandidates {
				if normalizeModelID(candidate.ID) == normalizeModelID(local.ModelID) {
					item.Candidates = append(item.Candidates, candidate)
				}
			}
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{
		"items":         items,
		"candidates":    allCandidates,
		"catalog_count": len(catalog),
	})
}

type openRouterSyncRequest struct {
	Mappings              []openRouterMapping `json:"mappings"`
	Fields                []string            `json:"fields"`
	IncludePricing        bool                `json:"include_pricing"`
	OverwriteUserModified bool                `json:"overwrite_user_modified"`
}

type openRouterMapping struct {
	LocalModelID int64  `json:"local_model_id"`
	OpenRouterID string `json:"openrouter_id"`
}

func (h *OpenRouterHandler) Sync(c *gin.Context) {
	var req openRouterSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Mappings) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供已确认的 OpenRouter 模型映射"})
		return
	}
	if len(req.Fields) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请至少选择一个同步字段"})
		return
	}
	catalog, err := openrouter.Fetch(upstream.NewHTTPClient())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	catalogByID := make(map[string]openrouter.Model, len(catalog))
	for _, model := range catalog {
		catalogByID[model.ID] = model
	}

	updates := make([]store.OpenRouterModelUpdate, 0, len(req.Mappings))
	for _, mapping := range req.Mappings {
		model, ok := catalogByID[mapping.OpenRouterID]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "OpenRouter 模型不存在: " + mapping.OpenRouterID})
			return
		}
		local, err := h.modelRepo.GetByID(mapping.LocalModelID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "本地模型不存在: " + strconv.FormatInt(mapping.LocalModelID, 10)})
			return
		}
		if local.UserModified && !req.OverwriteUserModified {
			continue
		}
		updates = append(updates, store.OpenRouterModelUpdate{
			ModelID: mapping.LocalModelID,
			Info: store.OpenRouterModelInfo{
				DisplayName: model.Name, ContextWindow: model.ContextLength,
				MaxOutputTokens:  model.TopProvider.MaxCompletionTokens,
				SupportsVision:   model.ToInfo(false).SupportsVision,
				SupportsThinking: model.ToInfo(false).SupportsThinking,
				SupportsTools:    model.ToInfo(false).SupportsTools,
				Capabilities:     model.ToInfo(false).Capabilities,
				InputModalities:  model.Architecture.InputModalities,
				OutputModalities: model.Architecture.OutputModalities,
			},
			PricingInput: modelPrice(model, 0), PricingOutput: modelPrice(model, 1),
			PricingCacheRead: modelPrice(model, 2), PricingCacheWrite: modelPrice(model, 3),
			Fields: req.Fields, IncludePricing: req.IncludePricing,
		})
	}
	if err := h.modelRepo.ApplyOpenRouterUpdates(updates); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.modelRepo.InvalidateModelCache()
	c.JSON(http.StatusOK, gin.H{"updated": len(updates), "skipped": len(req.Mappings) - len(updates)})
}

func candidateFromOpenRouter(model openrouter.Model) openRouterCandidate {
	info := model.ToInfo(false)
	input, output, cacheRead, cacheWrite := model.PricesPerMillion()
	return openRouterCandidate{ID: model.ID, Name: model.Name, ContextLength: model.ContextLength,
		MaxOutputTokens: model.TopProvider.MaxCompletionTokens, InputModalities: info.InputModalities,
		OutputModalities: info.OutputModalities, SupportsVision: info.SupportsVision,
		SupportsThinking: info.SupportsThinking, SupportsTools: info.SupportsTools,
		Capabilities: info.Capabilities, PricingInput: input, PricingOutput: output,
		PricingCacheRead: cacheRead, PricingCacheWrite: cacheWrite}
}

func normalizeModelID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, prefix := range []string{"openai/", "anthropic/", "google/", "qwen/", "deepseek/", "x-ai/", "z-ai/"} {
		id = strings.TrimPrefix(id, prefix)
	}
	return openrouter.NormalizeID(id)
}

func modelPrice(model openrouter.Model, index int) float64 {
	input, output, cacheRead, cacheWrite := model.PricesPerMillion()
	prices := []float64{input, output, cacheRead, cacheWrite}
	if index >= 0 && index < len(prices) {
		return prices[index]
	}
	return 0
}
