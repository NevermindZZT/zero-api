package store

import "testing"

func TestModel_EffectiveProtocols(t *testing.T) {
	m := &Model{Protocols: []string{"openai", "responses"}}
	got := m.EffectiveProtocols("openai")
	if len(got) != 2 || got[0] != "openai" || got[1] != "responses" {
		t.Fatalf("模型声明应优先，got %v", got)
	}
	m2 := &Model{Protocols: nil}
	got2 := m2.EffectiveProtocols("anthropic")
	if len(got2) != 1 || got2[0] != "anthropic" {
		t.Fatalf("空应继承渠道，got %v", got2)
	}
	m3 := &Model{Protocols: nil}
	got3 := m3.EffectiveProtocols("")
	if len(got3) != 1 || got3[0] != "openai" {
		t.Fatalf("空渠道默认 openai，got %v", got3)
	}
}

func TestModel_SupportsProtocol(t *testing.T) {
	m := &Model{Protocols: []string{"responses"}}
	if !m.SupportsProtocol("responses", "openai") {
		t.Fatal("responses 应被支持")
	}
	if m.SupportsProtocol("openai", "openai") {
		t.Fatal("未声明的 openai 不应被支持")
	}
	m2 := &Model{Protocols: nil}
	if !m2.SupportsProtocol("openai", "openai") {
		t.Fatal("继承渠道 openai 应被支持")
	}
}

func TestModel_ProtocolURL(t *testing.T) {
	// 未单独配置时回退渠道 base_url 拼接
	m := &Model{Protocols: []string{"anthropic"}}
	if got := m.ProtocolURL("anthropic", "https://api.xxx.com/v1"); got != "https://api.xxx.com/v1/messages" {
		t.Fatalf("未配置时应拼接渠道 base_url，got %s", got)
	}
	// 单独配置优先
	m2 := &Model{ProtocolURLs: map[string]string{
		"anthropic": "https://api.xxx.com/anthropic/v1/messages",
	}}
	if got := m2.ProtocolURL("anthropic", "https://api.xxx.com/v1"); got != "https://api.xxx.com/anthropic/v1/messages" {
		t.Fatalf("单独配置应优先，got %s", got)
	}
	// 未配置该协议的 key 时回退
	m3 := &Model{ProtocolURLs: map[string]string{
		"openai": "https://special.example.com/v1/chat/completions",
	}}
	if got := m3.ProtocolURL("responses", "https://api.xxx.com/v1"); got != "https://api.xxx.com/v1/responses" {
		t.Fatalf("未配置的协议应回退拼接，got %s", got)
	}
	// 配置为空字符串视为未配置
	m4 := &Model{ProtocolURLs: map[string]string{"openai": ""}}
	if got := m4.ProtocolURL("openai", "https://api.xxx.com/v1"); got != "https://api.xxx.com/v1/chat/completions" {
		t.Fatalf("空字符串应回退拼接，got %s", got)
	}
}
