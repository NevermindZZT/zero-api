package cpa

import (
	"strings"
	"testing"
)

func TestRenderIncludesLocalManagementAPI(t *testing.T) {
	content, err := (&Config{
		Host: "127.0.0.1", Port: 8317, APIKeys: []string{"api-key"}, ManagementKey: "management-secret",
	}).Render()
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{"remote-management:", "allow-remote: false", "secret-key: management-secret"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
}
