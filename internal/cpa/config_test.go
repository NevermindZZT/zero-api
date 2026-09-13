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

func TestRenderAllowsRemoteManagement(t *testing.T) {
	content, err := (&Config{AllowRemote: true, ManagementKey: "management-secret"}).Render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "allow-remote: true") {
		t.Fatalf("config missing enabled remote management:\n%s", content)
	}
}
