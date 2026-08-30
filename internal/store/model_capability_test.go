package store

import "testing"

func TestModelCapabilityFallbackAndImageCapabilities(t *testing.T) {
	m := &Model{SupportsVision: true, SupportsTools: true}
	if !m.SupportsCapability("vision") || !m.SupportsCapability("tool_calling") {
		t.Fatal("legacy capability fields should remain compatible")
	}
	m.Capabilities = []string{"responses", "image_generation"}
	if !m.SupportsCapability("image_generation") || !m.SupportsCapability("responses") {
		t.Fatal("declared capabilities should be supported")
	}
	if m.SupportsCapability("image_editing") {
		t.Fatal("undeclared capability should not be supported")
	}
}
