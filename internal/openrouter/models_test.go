package openrouter

import (
	"encoding/json"
	"testing"
)

func TestModelToInfoAndPrices(t *testing.T) {
	m := Model{
		ID:                  "openai/gpt-5-image",
		Name:                "GPT-5 Image",
		ContextLength:       400000,
		Architecture:        Architecture{InputModalities: []string{"text", "image"}, OutputModalities: []string{"text", "image"}},
		Pricing:             Pricing{Prompt: "0.0000025", Completion: "0.00001", InputCacheRead: "0.00000025"},
		SupportedParameters: []string{"tools", "reasoning_effort"},
	}
	info := m.ToInfo(false)
	if !info.SupportsVision || !info.SupportsTools || !info.SupportsThinking {
		t.Fatalf("capabilities were not inferred: %+v", info)
	}
	if !contains(info.Capabilities, "image_generation") {
		t.Fatalf("image generation capability missing: %+v", info.Capabilities)
	}
	input, output, cacheRead, cacheWrite := m.PricesPerMillion()
	if input != 2.5 || output != 10 || cacheRead != 0.25 || cacheWrite != 0 {
		t.Fatalf("prices = %v, %v, %v, %v", input, output, cacheRead, cacheWrite)
	}
}

func TestPerMillionRejectsInvalidAndNegative(t *testing.T) {
	if got := perMillion(json.Number("-1")); got != 0 {
		t.Fatalf("negative price = %v, want 0", got)
	}
	if got := perMillion(json.Number("invalid")); got != 0 {
		t.Fatalf("invalid price = %v, want 0", got)
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
