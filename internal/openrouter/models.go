package openrouter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/never/zero-api/internal/adapter"
)

const ModelsURL = "https://openrouter.ai/api/v1/models"

type Catalog struct {
	Data []Model `json:"data"`
}

type Model struct {
	ID                  string       `json:"id"`
	Name                string       `json:"name"`
	ContextLength       int          `json:"context_length"`
	Architecture        Architecture `json:"architecture"`
	Pricing             Pricing      `json:"pricing"`
	TopProvider         TopProvider  `json:"top_provider"`
	SupportedParameters []string     `json:"supported_parameters"`
	Reasoning           *Reasoning   `json:"reasoning"`
}

type Architecture struct {
	Modality         string   `json:"modality"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

type Pricing struct {
	Prompt          json.Number `json:"prompt"`
	Completion      json.Number `json:"completion"`
	InputCacheRead  json.Number `json:"input_cache_read"`
	InputCacheWrite json.Number `json:"input_cache_write"`
}

type TopProvider struct {
	ContextLength       int `json:"context_length"`
	MaxCompletionTokens int `json:"max_completion_tokens"`
}

type Reasoning struct {
	Mandatory bool `json:"mandatory"`
}

func Fetch(client *http.Client) ([]Model, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequest(http.MethodGet, ModelsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 OpenRouter 模型目录失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("OpenRouter 返回 HTTP %d", resp.StatusCode)
	}
	var catalog Catalog
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("解析 OpenRouter 模型目录失败: %w", err)
	}
	return catalog.Data, nil
}

func (m Model) ToInfo(includePricing bool) adapter.ModelInfo {
	_ = includePricing
	info := adapter.ModelInfo{
		ID: m.ID, Name: m.Name, ContextWindow: m.ContextLength,
		MaxOutputTokens:  m.TopProvider.MaxCompletionTokens,
		InputModalities:  append([]string(nil), m.Architecture.InputModalities...),
		OutputModalities: append([]string(nil), m.Architecture.OutputModalities...),
	}
	for _, modality := range info.InputModalities {
		if modality == "image" {
			info.SupportsVision = true
			addUnique(&info.Capabilities, "vision")
		}
	}
	for _, p := range m.SupportedParameters {
		switch p {
		case "tools", "tool_choice":
			info.SupportsTools = true
			addUnique(&info.Capabilities, "tool_calling")
		case "reasoning", "reasoning_effort", "include_reasoning":
			info.SupportsThinking = true
			addUnique(&info.Capabilities, "thinking")
		}
	}
	if m.Reasoning != nil {
		info.SupportsThinking = true
		addUnique(&info.Capabilities, "thinking")
	}
	for _, modality := range info.OutputModalities {
		if modality == "image" {
			addUnique(&info.Capabilities, "image_generation")
		}
	}
	if includePricing {
		// Pricing is applied by the caller because ModelInfo deliberately has no price fields.
	}
	return info
}

func (m Model) PricesPerMillion() (input, output, cacheRead, cacheWrite float64) {
	input = perMillion(m.Pricing.Prompt)
	output = perMillion(m.Pricing.Completion)
	cacheRead = perMillion(m.Pricing.InputCacheRead)
	cacheWrite = perMillion(m.Pricing.InputCacheWrite)
	return
}

func perMillion(v json.Number) float64 {
	f, err := strconv.ParseFloat(string(v), 64)
	if err != nil || f < 0 {
		return 0
	}
	return f * 1000000
}

func addUnique(dst *[]string, value string) {
	for _, existing := range *dst {
		if existing == value {
			return
		}
	}
	*dst = append(*dst, value)
}

func NormalizeID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	for _, suffix := range []string{":free", ":batch", ":nitro", ":exacto", ":online", ":floor"} {
		id = strings.TrimSuffix(id, suffix)
	}
	return id
}
