package router

import (
	"encoding/json"
	"strings"
	"testing"
)

// ===== OpenAI 协议图片处理 =====

const openAIReq = `{
  "model": "text-vision",
  "messages": [
    {"role": "user", "content": "这是什么？"},
    {"role": "user", "content": [
      {"type": "text", "text": "看这张图"},
      {"type": "image_url", "image_url": {"url": "https://example.com/a.png"}},
      {"type": "image_url", "image_url": {"url": "https://example.com/b.png"}}
    ]}
  ]
}`

func TestExtractImagesOpenAI(t *testing.T) {
	refs := ExtractImages(ProtocolOpenAI, []byte(openAIReq))
	if len(refs) != 2 {
		t.Fatalf("应提取 2 张图片，got %d", len(refs))
	}
	if refs[0].UsableURL() != "https://example.com/a.png" {
		t.Errorf("URL 提取错误: %s", refs[0].UsableURL())
	}
}

func TestReplaceImagesOpenAI(t *testing.T) {
	descs := []string{"一只猫", "一只狗"}
	out := ReplaceImages(ProtocolOpenAI, []byte(openAIReq), "deepseek-v4-flash", descs)
	if out == nil {
		t.Fatal("替换失败")
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["model"] != "deepseek-v4-flash" {
		t.Errorf("model 未替换: %v", parsed["model"])
	}
	msgs := parsed["messages"].([]interface{})
	content := msgs[1].(map[string]interface{})["content"]
	raw, _ := json.Marshal(content)
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL json.RawMessage `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		t.Fatal(err)
	}
	// 3 块：text + 2 个图片描述
	if len(parts) != 3 {
		t.Fatalf("应保留 3 个文本块，got %d: %s", len(parts), string(raw))
	}
	if parts[1].Type != "text" || !strings.Contains(parts[1].Text, "一只猫") {
		t.Errorf("图片1替换错误: %+v", parts[1])
	}
	if parts[2].Type != "text" || !strings.Contains(parts[2].Text, "一只狗") {
		t.Errorf("图片2替换错误: %+v", parts[2])
	}
	// 描述块不应残留 image_url 空对象
	if len(parts[1].ImageURL) > 0 || len(parts[2].ImageURL) > 0 {
		t.Errorf("text 块不应带 image_url: %+v %+v", parts[1], parts[2])
	}
}

// TestVisionInstructionInjection 验证识图指令注入（禁止主模型读图/调工具看图）
func TestVisionInstructionInjection(t *testing.T) {
	descs := []string{"一只猫"}
	// 模拟成功路径：第一条描述前注入 VisionInstruction
	withInstr := VisionInstruction + "\n" + descs[0]
	out := ReplaceImages(ProtocolOpenAI, []byte(openAIReq), "deepseek-v4-flash", []string{withInstr})
	if out == nil {
		t.Fatal("替换失败")
	}
	var parsed map[string]interface{}
	json.Unmarshal(out, &parsed)
	msgs := parsed["messages"].([]interface{})
	content := msgs[1].(map[string]interface{})["content"]
	raw, _ := json.Marshal(content)
	if !strings.Contains(string(raw), "不要尝试读取任何图片文件") {
		t.Error("替换结果应包含禁止读图指令")
	}
	if !strings.Contains(string(raw), "一只猫") {
		t.Error("替换结果应包含识图描述")
	}

	// 模拟失败路径：VisionFailureInstruction 注入
	out2 := ReplaceImages(ProtocolOpenAI, []byte(openAIReq), "deepseek-v4-flash",
		[]string{VisionFailureInstruction + "（错误: test err）"})
	if out2 == nil {
		t.Fatal("失败路径替换失败")
	}
	json.Unmarshal(out2, &parsed)
	msgs = parsed["messages"].([]interface{})
	content = msgs[1].(map[string]interface{})["content"]
	raw, _ = json.Marshal(content)
	if !strings.Contains(string(raw), "图片识别失败") || !strings.Contains(string(raw), "不要尝试任何其他方式") {
		t.Error("失败路径应包含失败告知+禁止其他手段指令")
	}
}

// ===== Anthropic 协议图片处理 =====

const anthropicReq = `{
  "model": "text-vision",
  "messages": [
    {"role": "user", "content": "这是什么？"},
    {"role": "user", "content": [
      {"type": "text", "text": "看这张图"},
      {"type": "image", "source": {"type": "url", "url": "https://example.com/a.png"}},
      {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "iVBORw0KGgoAAAANSUhEUg=="}}
    ]}
  ]
}`

func TestExtractImagesAnthropic(t *testing.T) {
	refs := ExtractImages(ProtocolAnthropic, []byte(anthropicReq))
	if len(refs) != 2 {
		t.Fatalf("应提取 2 张图片，got %d", len(refs))
	}
	if refs[0].UsableURL() != "https://example.com/a.png" {
		t.Errorf("url 图片提取错误: %s", refs[0].UsableURL())
	}
	// base64 图片应转为 data URL
	expected := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg=="
	if refs[1].UsableURL() != expected {
		t.Errorf("base64 图片转 data URL 错误:\n got %s\nwant %s", refs[1].UsableURL(), expected)
	}
}

func TestReplaceImagesAnthropic(t *testing.T) {
	descs := []string{"一只猫"}
	out := ReplaceImages(ProtocolAnthropic, []byte(anthropicReq), "deepseek-v4-flash", descs)
	if out == nil {
		t.Fatal("替换失败")
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	msgs := parsed["messages"].([]interface{})
	content := msgs[1].(map[string]interface{})["content"]
	raw, _ := json.Marshal(content)
	var blocks []map[string]interface{}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 3 {
		t.Fatalf("应保留 3 个块，got %d", len(blocks))
	}
	// 图片块应替换为 text 块
	if blocks[1]["type"] != "text" {
		t.Errorf("图片块未替换: %v", blocks[1])
	}
	if blocks[2]["type"] != "text" {
		t.Errorf("base64 图片块未替换: %v", blocks[2])
	}
}

// ===== Responses 协议图片处理 =====

const responsesReq = `{
  "model": "text-vision",
  "input": [
    {"role": "user", "content": "这是什么？"},
    {"role": "user", "content": [
      {"type": "input_text", "text": "看这张图"},
      {"type": "input_image", "image_url": "https://example.com/a.png"},
      {"type": "input_image", "image_url": "https://example.com/b.png"}
    ]}
  ]
}`

func TestExtractImagesResponses(t *testing.T) {
	refs := ExtractImages(ProtocolResponses, []byte(responsesReq))
	if len(refs) != 2 {
		t.Fatalf("应提取 2 张图片，got %d", len(refs))
	}
	if refs[1].UsableURL() != "https://example.com/b.png" {
		t.Errorf("URL 提取错误: %s", refs[1].UsableURL())
	}
}

func TestReplaceImagesResponses(t *testing.T) {
	descs := []string{"一只猫", "一只狗"}
	out := ReplaceImages(ProtocolResponses, []byte(responsesReq), "deepseek-v4-flash", descs)
	if out == nil {
		t.Fatal("替换失败")
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["model"] != "deepseek-v4-flash" {
		t.Errorf("model 未替换")
	}
	input := parsed["input"].([]interface{})
	content := input[1].(map[string]interface{})["content"]
	raw, _ := json.Marshal(content)
	var blocks []map[string]interface{}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 3 {
		t.Fatalf("应保留 3 个块，got %d", len(blocks))
	}
	if blocks[1]["type"] != "input_text" {
		t.Errorf("图片块未替换为 input_text: %v", blocks[1])
	}
}

// ===== ReplaceModel =====

func TestReplaceModel(t *testing.T) {
	out := ReplaceModel([]byte(`{"model":"text-vision","messages":[]}`), "deepseek-v4-flash")
	if out == nil {
		t.Fatal("替换失败")
	}
	var parsed map[string]interface{}
	json.Unmarshal(out, &parsed)
	if parsed["model"] != "deepseek-v4-flash" {
		t.Errorf("model 未替换: %v", parsed["model"])
	}
}

// ===== 无图请求（仅替换模型名，保持消息结构不变） =====

func TestReplaceModelNoImage(t *testing.T) {
	body := `{"model":"text-vision","messages":[{"role":"user","content":"你好"}]}`
	images := ExtractImages(ProtocolOpenAI, []byte(body))
	if len(images) != 0 {
		t.Fatalf("纯文本请求不应有图片，got %d", len(images))
	}
	out := ReplaceModel([]byte(body), "deepseek-v4-flash")
	if out == nil {
		t.Fatal("替换失败")
	}
	var parsed map[string]interface{}
	json.Unmarshal(out, &parsed)
	msgs := parsed["messages"].([]interface{})
	content := msgs[0].(map[string]interface{})["content"]
	if content != "你好" {
		t.Errorf("字符串 content 不应被修改: %v", content)
	}
}

// ===== ApplyRules 框架 =====

// testRule 简单测试规则
type testRule struct {
	name    string
	matches bool
	newBody []byte
	handled bool
}

func (r *testRule) Name() string { return r.name }
func (r *testRule) Match(ctx *Context) bool {
	return r.matches && ctx.Model == "hit-me"
}
func (r *testRule) Transform(ctx *Context) *Result {
	if r.handled {
		return ErrorResult(403, "规则接管")
	}
	return &Result{NewBody: r.newBody}
}

func TestApplyRules(t *testing.T) {
	ctx := &Context{Model: "hit-me", RawBody: []byte(`{"model":"hit-me"}`)}

	// 规则1不匹配，规则2命中返回 NewBody
	rules := []Rule{
		&testRule{name: "a", matches: false},
		&testRule{name: "b", matches: true, newBody: []byte(`{"model":"real"}`)},
	}
	res := ApplyRules(rules, ctx)
	if res == nil || res.Handled || string(res.NewBody) != `{"model":"real"}` {
		t.Fatalf("ApplyRules 结果错误: %+v", res)
	}

	// 规则命中 handled
	rules = []Rule{
		&testRule{name: "c", matches: true, handled: true},
	}
	res = ApplyRules(rules, ctx)
	if res == nil || !res.Handled || res.StatusCode != 403 {
		t.Fatalf("handled 结果错误: %+v", res)
	}

	// 无规则命中
	rules = []Rule{&testRule{name: "d", matches: false}}
	if res := ApplyRules(rules, ctx); res != nil {
		t.Fatalf("应返回 nil: %+v", res)
	}
}

// ===== 多轮会话：历史图片不触发识图，仅最后一条 user 消息的图片触发 =====

// 多轮会话请求：第一轮带图（历史），第二轮（最后一条 user）无图
const multiTurnReq = `{
  "model": "text-vision",
  "messages": [
    {"role": "user", "content": [
      {"type": "text", "text": "第一轮带图"},
      {"type": "image_url", "image_url": {"url": "https://example.com/history.png"}}
    ]},
    {"role": "assistant", "content": "第一轮的回复"},
    {"role": "user", "content": "第二轮纯文本追问"}
  ]
}`

func TestExtractLatestUserImagesMultiTurn(t *testing.T) {
	// 全部消息提取：2 张图（含历史）
	all := ExtractImages(ProtocolOpenAI, []byte(multiTurnReq))
	if len(all) != 1 {
		t.Fatalf("全部消息应提取 1 张图（历史图），got %d", len(all))
	}
	// 最后一条 user：0 张图（不触发识图）
	latest := ExtractLatestUserImages(ProtocolOpenAI, []byte(multiTurnReq))
	if len(latest) != 0 {
		t.Fatalf("最后一条 user 应无图（不触发识图），got %d", len(latest))
	}
}

func TestReplaceImagesMultiTurnHistoryPlaceholder(t *testing.T) {
	// 无图触发（nil 描述）：历史图片应替换为占位文本，不残留原始图片
	out := ReplaceImages(ProtocolOpenAI, []byte(multiTurnReq), "deepseek-v4-flash", nil)
	if out == nil {
		t.Fatal("替换失败")
	}
	var parsed map[string]interface{}
	json.Unmarshal(out, &parsed)
	msgs := parsed["messages"].([]interface{})
	// 第一条 user（历史带图）：应替换为占位
	content0 := msgs[0].(map[string]interface{})["content"]
	raw0, _ := json.Marshal(content0)
	if !strings.Contains(string(raw0), historyImagePlaceholder) {
		t.Errorf("历史图片应替换为占位文本: %s", string(raw0))
	}
	if strings.Contains(string(raw0), "image_url") {
		t.Errorf("历史图片不应残留 image_url: %s", string(raw0))
	}
	// 最后一条 user（纯文本）：content 保持字符串不变
	content2 := msgs[2].(map[string]interface{})["content"]
	if content2 != "第二轮纯文本追问" {
		t.Errorf("纯文本 user 消息不应被修改: %v", content2)
	}
}

// 单轮带图（最后一条 user 有图）：ExtractLatestUserImages 应提取
func TestExtractLatestUserImagesSingleTurn(t *testing.T) {
	latest := ExtractLatestUserImages(ProtocolOpenAI, []byte(openAIReq))
	if len(latest) != 2 {
		t.Fatalf("单轮最后一条 user 应提取 2 张图，got %d", len(latest))
	}
}

// Anthropic 多轮
const anthropicMultiTurnReq = `{
  "model": "text-vision",
  "messages": [
    {"role": "user", "content": [
      {"type": "text", "text": "第一轮"},
      {"type": "image", "source": {"type": "url", "url": "https://example.com/h.png"}}
    ]},
    {"role": "assistant", "content": [{"type": "text", "text": "回复"}]},
    {"role": "user", "content": [{"type": "text", "text": "第二轮"}]}
  ]
}`

func TestExtractLatestUserImagesAnthropicMultiTurn(t *testing.T) {
	latest := ExtractLatestUserImages(ProtocolAnthropic, []byte(anthropicMultiTurnReq))
	if len(latest) != 0 {
		t.Fatalf("Anthropic 最后一条 user 应无图，got %d", len(latest))
	}
	out := ReplaceImages(ProtocolAnthropic, []byte(anthropicMultiTurnReq), "deepseek-v4-flash", nil)
	if out == nil {
		t.Fatal("替换失败")
	}
	var parsed map[string]interface{}
	json.Unmarshal(out, &parsed)
	msgs := parsed["messages"].([]interface{})
	content0 := msgs[0].(map[string]interface{})["content"]
	raw0, _ := json.Marshal(content0)
	if !strings.Contains(string(raw0), historyImagePlaceholder) {
		t.Errorf("Anthropic 历史图片应替换为占位: %s", string(raw0))
	}
}

// Responses 多轮
const responsesMultiTurnReq = `{
  "model": "text-vision",
  "input": [
    {"role": "user", "content": [
      {"type": "input_text", "text": "第一轮"},
      {"type": "input_image", "image_url": "https://example.com/h.png"}
    ]},
    {"role": "assistant", "content": [{"type": "output_text", "text": "回复"}]},
    {"role": "user", "content": [{"type": "input_text", "text": "第二轮"}]}
  ]
}`

func TestExtractLatestUserImagesResponsesMultiTurn(t *testing.T) {
	latest := ExtractLatestUserImages(ProtocolResponses, []byte(responsesMultiTurnReq))
	if len(latest) != 0 {
		t.Fatalf("Responses 最后一条 user 应无图，got %d", len(latest))
	}
	out := ReplaceImages(ProtocolResponses, []byte(responsesMultiTurnReq), "deepseek-v4-flash", nil)
	if out == nil {
		t.Fatal("替换失败")
	}
	var parsed map[string]interface{}
	json.Unmarshal(out, &parsed)
	input := parsed["input"].([]interface{})
	content0 := input[0].(map[string]interface{})["content"]
	raw0, _ := json.Marshal(content0)
	if !strings.Contains(string(raw0), historyImagePlaceholder) {
		t.Errorf("Responses 历史图片应替换为占位: %s", string(raw0))
	}
}

// ===== Responses 协议省略 role（OpenAI SDK 常见） =====

// 省略 role 的 input item：type=message（缺省 role=user），第二轮带图
const responsesNoRoleReq = `{
  "model": "text-vision",
  "input": [
    {"type": "message", "content": [{"type": "input_text", "text": "第一轮文本"}]},
    {"type": "message", "content": [
      {"type": "input_text", "text": "第二轮带图"},
      {"type": "input_image", "image_url": "https://example.com/new.png"}
    ]}
  ]
}`

func TestExtractLatestUserImagesResponsesNoRole(t *testing.T) {
	// 省略 role 时也应识别最后一条 message 中的图片
	latest := ExtractLatestUserImages(ProtocolResponses, []byte(responsesNoRoleReq))
	if len(latest) != 1 {
		t.Fatalf("省略 role 的 input 应提取 1 张图，got %d", len(latest))
	}
	// 替换：最后一条 message 图片用描述
	out := ReplaceImages(ProtocolResponses, []byte(responsesNoRoleReq), "deepseek-v4-flash", []string{"新图描述"})
	if out == nil {
		t.Fatal("替换失败")
	}
	var parsed map[string]interface{}
	json.Unmarshal(out, &parsed)
	input := parsed["input"].([]interface{})
	// 第二条（带图）→ 应替换为描述
	c1 := input[1].(map[string]interface{})["content"]
	raw1, _ := json.Marshal(c1)
	if !strings.Contains(string(raw1), "新图描述") {
		t.Errorf("带图 input 应替换为描述: %s", string(raw1))
	}
	if strings.Contains(string(raw1), "image_url") || strings.Contains(string(raw1), "input_image") {
		t.Errorf("带图 input 不应残留图片: %s", string(raw1))
	}
}

// ===== 本轮新增图片判定（agent 多轮） =====

// 场景1：带图 user 后无 assistant 回复（本轮新图）→ 应触发
const agentNewImageReq = `{
  "model": "text-vision",
  "messages": [
    {"role": "user", "content": "第一轮"},
    {"role": "assistant", "content": "回复1"},
    {"role": "user", "content": [
      {"type": "text", "text": "看图"},
      {"type": "image_url", "image_url": {"url": "https://example.com/new.png"}}
    ]},
    {"role": "user", "content": "请详细描述"}
  ]
}`

// 场景2：带图 user 后有 assistant 回复（历史图片）→ 不触发
const agentHistoryImageReq = `{
  "model": "text-vision",
  "messages": [
    {"role": "user", "content": [
      {"type": "text", "text": "看图"},
      {"type": "image_url", "image_url": {"url": "https://example.com/old.png"}}
    ]},
    {"role": "assistant", "content": "回复1"},
    {"role": "user", "content": "追问"}
  ]
}`

func TestExtractLatestUserImagesAgentNewImage(t *testing.T) {
	// 带图 user 后无 assistant 回复 → 触发（用户本次诉求）
	refs := ExtractLatestUserImages(ProtocolOpenAI, []byte(agentNewImageReq))
	if len(refs) != 1 {
		t.Fatalf("本轮新图应触发识图，got %d", len(refs))
	}
}

func TestExtractLatestUserImagesAgentHistoryImage(t *testing.T) {
	// 带图 user 后有 assistant 回复 → 不触发（用户上次诉求：历史不重复识别）
	refs := ExtractLatestUserImages(ProtocolOpenAI, []byte(agentHistoryImageReq))
	if len(refs) != 0 {
		t.Fatalf("历史图片不应触发识图，got %d", len(refs))
	}
}

// ===== 字符串形式 image_url（部分 SDK/框架格式） =====

const stringImageURLReq = `{
  "model": "text-vision",
  "messages": [
    {"role": "user", "content": [
      {"type": "text", "text": "看图"},
      {"type": "image_url", "image_url": "data:image/png;base64,AAA"}
    ]}
  ]
}`

func TestExtractImagesStringImageURL(t *testing.T) {
	// 字符串形式 image_url 应能提取
	refs := ExtractLatestUserImages(ProtocolOpenAI, []byte(stringImageURLReq))
	if len(refs) != 1 {
		t.Fatalf("字符串 image_url 应提取 1 张图，got %d", len(refs))
	}
	// 替换后应无图片残留
	out := ReplaceImages(ProtocolOpenAI, []byte(stringImageURLReq), "deepseek-v4-flash", []string{"描述"})
	if out == nil {
		t.Fatal("替换失败")
	}
	if len(ExtractImages(ProtocolOpenAI, out)) != 0 {
		t.Error("替换后不应残留图片")
	}
	if !strings.Contains(string(out), "描述") {
		t.Error("替换后应含描述")
	}
}
