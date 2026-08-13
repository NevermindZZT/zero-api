package router

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// ImageRef 图片引用（三种下游协议统一表示）
type ImageRef struct {
	URL       string // 图片 URL（openai image_url / anthropic source.url / responses image_url）
	Base64    string // base64 数据（anthropic source.data）
	MediaType string // 媒体类型（anthropic source.media_type）
	// 识图请求可用的 URL（base64 已转为 data URL）
	usableURL string
}

// UsableURL 返回可用于识图请求的图片 URL
// base64 图片转为 data URL（OpenAI 兼容格式）
func (r *ImageRef) UsableURL() string {
	if r.usableURL != "" {
		return r.usableURL
	}
	if r.URL != "" {
		return r.URL
	}
	if r.Base64 != "" {
		mt := r.MediaType
		if mt == "" {
			mt = "image/jpeg"
		}
		return fmt.Sprintf("data:%s;base64,%s", mt, r.Base64)
	}
	return ""
}

// ExtractImages 按下游协议提取请求中的图片
func ExtractImages(protocol string, body []byte) []ImageRef {
	switch protocol {
	case ProtocolAnthropic:
		return extractImagesAnthropic(body)
	case ProtocolResponses:
		return extractImagesResponses(body)
	default:
		return extractImagesOpenAI(body)
	}
}

// ExtractLatestUserImages 按下游协议提取【最后一条 user 消息】中的图片。
// 用于触发识图判断：多轮会话时客户端会携带完整历史（含第一轮的图片消息），
// 但只有最后一条 user 消息（当前轮次的输入）才是需要识图的新图片，
// 历史消息中的图片已有上一轮 assistant 回复覆盖，不应重复触发识图。
func ExtractLatestUserImages(protocol string, body []byte) []ImageRef {
	switch protocol {
	case ProtocolAnthropic:
		return extractLatestUserImagesAnthropic(body)
	case ProtocolResponses:
		return extractLatestUserImagesResponses(body)
	default:
		return extractLatestUserImagesOpenAI(body)
	}
}

// logMessageImageProfile 打印请求中每条消息的角色 + 图片数（调试多轮识图触发用）
func logMessageImageProfile(protocol string, body []byte) {
	switch protocol {
	case ProtocolAnthropic:
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return
		}
		for i, m := range req.Messages {
			log.Printf("[路由:虚拟模型]  消息[%d] role=%s 图片数=%d", i, m.Role, len(extractAnthropicContentImages(m.Content)))
		}
	case ProtocolResponses:
		var req struct {
			Input []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"input"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return
		}
		for i, item := range req.Input {
			log.Printf("[路由:虚拟模型]  input[%d] role=%s 图片数=%d", i, item.Role, len(extractResponsesContentImages(item.Content)))
		}
	default:
		var req struct {
			Messages []openAIChatMessage `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return
		}
		for i, m := range req.Messages {
			log.Printf("[路由:虚拟模型]  消息[%d] role=%s 图片数=%d", i, m.Role, len(extractOpenAIContentImages(m.Content)))
		}
	}
}

// historyImagePlaceholder 历史 user 消息中图片的占位文本（不触发识图、不透传给主模型）
const historyImagePlaceholder = "[历史消息中的图片：内容已在此前对话中描述]"

// ReplaceModel 替换请求中的 model 字段（三种协议均为顶层 model 字段）
func ReplaceModel(body []byte, toModel string) []byte {
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	parsed["model"] = toModel
	modified, err := json.Marshal(parsed)
	if err != nil {
		return nil
	}
	return modified
}

// ReplaceImages 将请求中的图片替换为文本描述（保持文本块顺序，图片块原位替换为描述）
// toModel 替换后的模型名；descriptions 每个元素对应一张图片的描述
func ReplaceImages(protocol string, body []byte, toModel string, descriptions []string) []byte {
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	parsed["model"] = toModel

	switch protocol {
	case ProtocolAnthropic:
		replaceAnthropicImages(parsed, descriptions)
	case ProtocolResponses:
		replaceResponsesImages(parsed, descriptions)
	default:
		replaceOpenAIImages(parsed, descriptions)
	}

	modified, err := json.Marshal(parsed)
	if err != nil {
		return nil
	}
	return modified
}

// ===== OpenAI chat 协议 =====

type openAIContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	// 指针类型：nil 时 omitempty 生效，text 块不会残留空的 image_url 对象
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

type openAIChatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func extractImagesOpenAI(body []byte) []ImageRef {
	var req struct {
		Messages []openAIChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil
	}
	var refs []ImageRef
	for _, m := range req.Messages {
		refs = append(refs, extractOpenAIContentImages(m.Content)...)
	}
	return refs
}

// extractLatestUserImagesOpenAI 提取【本轮新增】的图片
// 判断标准：从后往前找最后一条带图片的 user 消息——
//   - 若其后没有 assistant 回复（本轮新输入，如 agent 在带图 user 后又追加纯文本指令/工具结果）→ 触发识图
//   - 若其后已有 assistant 回复（历史图片已被回复覆盖）→ 不触发（避免重复识图）
//
// 注意：reasonix 等 reasoning agent 可能在带图 user 后追加 assistant 思考内容
// （role=assistant 但只是 reasoning 过程，非真正回复），此时也应触发——
// 见 extractLatestUserImagesOpenAI 的 assistantCovered 判定。
func extractLatestUserImagesOpenAI(body []byte) []ImageRef {
	var req struct {
		Messages []openAIChatMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != "user" {
			continue
		}
		refs := extractOpenAIContentImages(req.Messages[i].Content)
		if len(refs) == 0 {
			continue // 无图 user，继续往前找
		}
		// 找到带图 user：检查其后是否有 assistant 回复（历史覆盖）
		for j := i + 1; j < len(req.Messages); j++ {
			if req.Messages[j].Role == "assistant" {
				return nil // 已有回复覆盖 → 历史图片，不触发
			}
		}
		return refs // 本轮新图 → 触发
	}
	return nil
}

func extractOpenAIContentImages(content json.RawMessage) []ImageRef {
	if len(content) == 0 || string(content) == "null" {
		return nil
	}
	// 字符串形式无图片
	var str string
	if err := json.Unmarshal(content, &str); err == nil {
		return nil
	}
	// 用宽松结构解析 content 块（兼容 image_url 为对象或字符串两种形式）
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL json.RawMessage `json:"image_url"`
	}
	if err := json.Unmarshal(content, &parts); err != nil {
		return nil
	}
	var refs []ImageRef
	for _, p := range parts {
		if p.Type != "image_url" {
			continue
		}
		// image_url 可能为：{"url":"..."} 对象，或 "https://..." 字符串
		var urlStr string
		if err := json.Unmarshal(p.ImageURL, &urlStr); err == nil {
			if urlStr != "" {
				refs = append(refs, ImageRef{URL: urlStr, usableURL: urlStr})
			}
			continue
		}
		var urlObj struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(p.ImageURL, &urlObj); err == nil && urlObj.URL != "" {
			refs = append(refs, ImageRef{URL: urlObj.URL, usableURL: urlObj.URL})
		}
	}
	return refs
}

func replaceOpenAIImages(parsed map[string]interface{}, descriptions []string) {
	msgs, ok := parsed["messages"].([]interface{})
	if !ok {
		return
	}
	// 从后往前处理：第一条【含图】的 user 消息的所有图片分配描述（本轮新图），
	// 更早历史消息的图片分配占位（避免"最后一条 user 是纯文本指令、带图在倒数第二"的 agent 结构漏检，
	// 也避免历史图片重复识别/透传）
	imgIdx := 0
	latestMsgDone := false
	for i := len(msgs) - 1; i >= 0; i-- {
		mm, ok := msgs[i].(map[string]interface{})
		if !ok {
			continue
		}
		content, ok := mm["content"]
		if !ok {
			continue
		}
		raw, err := json.Marshal(content)
		if err != nil {
			continue
		}
		// content 为字符串形式 → 无图
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			continue
		}
		var blocks []map[string]interface{}
		if err := json.Unmarshal(raw, &blocks); err != nil {
			continue
		}
		// 本条消息是否含图（image_url 兼容对象/字符串形式）
		hasImg := false
		for _, b := range blocks {
			if t, _ := b["type"].(string); t == "image_url" && imageURLValue(rawOf(b, "image_url")) != "" {
				hasImg = true
				break
			}
		}
		if !hasImg {
			continue
		}
		// 从后往前第一条含图消息 → 描述；更早 → 占位
		isLatest := !latestMsgDone
		latestMsgDone = true
		changed := false
		var newBlocks []map[string]interface{}
		for _, b := range blocks {
			typ, _ := b["type"].(string)
			if typ == "image_url" && imageURLValue(rawOf(b, "image_url")) != "" {
				text := ""
				if isLatest {
					text = descAt(descriptions, imgIdx)
					imgIdx++
				} else {
					text = historyImagePlaceholder
				}
				newBlocks = append(newBlocks, map[string]interface{}{"type": "text", "text": text})
				changed = true
			} else {
				newBlocks = append(newBlocks, b) // 保留非图片块原样
			}
		}
		if changed {
			if b, err := json.Marshal(newBlocks); err == nil {
				mm["content"] = json.RawMessage(b)
				msgs[i] = mm
			}
		}
	}
	parsed["messages"] = msgs
}

// ===== Anthropic 协议 =====

type anthropicImageBlock struct {
	Type   string `json:"type"`
	Source struct {
		Type      string `json:"type"` // base64 | url
		MediaType string `json:"media_type,omitempty"`
		Data      string `json:"data,omitempty"`
		URL       string `json:"url,omitempty"`
	} `json:"source"`
}

type anthropicContentBlock struct {
	Type string          `json:"type"`
	Text string          `json:"text,omitempty"`
	Raw  json.RawMessage `json:"-"`
}

func extractImagesAnthropic(body []byte) []ImageRef {
	var req struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil
	}
	var refs []ImageRef
	for _, m := range req.Messages {
		refs = append(refs, extractAnthropicContentImages(m.Content)...)
	}
	return refs
}

// extractLatestUserImagesAnthropic 提取【本轮新增】的图片（带图 user 后无 assistant 回复才触发）
func extractLatestUserImagesAnthropic(body []byte) []ImageRef {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != "user" {
			continue
		}
		refs := extractAnthropicContentImages(req.Messages[i].Content)
		if len(refs) == 0 {
			continue
		}
		for j := i + 1; j < len(req.Messages); j++ {
			if req.Messages[j].Role == "assistant" {
				return nil
			}
		}
		return refs
	}
	return nil
}

func extractAnthropicContentImages(content json.RawMessage) []ImageRef {
	// 字符串形式无图片
	var str string
	if err := json.Unmarshal(content, &str); err == nil {
		return nil
	}
	var blocks []anthropicImageBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}
	var refs []ImageRef
	for _, b := range blocks {
		if b.Type != "image" {
			continue
		}
		switch b.Source.Type {
		case "base64":
			refs = append(refs, ImageRef{Base64: b.Source.Data, MediaType: b.Source.MediaType})
		case "url":
			if b.Source.URL != "" {
				refs = append(refs, ImageRef{URL: b.Source.URL, usableURL: b.Source.URL})
			}
		}
	}
	return refs
}

func replaceAnthropicImages(parsed map[string]interface{}, descriptions []string) {
	msgs, ok := parsed["messages"].([]interface{})
	if !ok {
		return
	}
	// 从后往前：第一条【含图】消息的所有图片分配描述，更早历史图片分配占位
	imgIdx := 0
	latestMsgDone := false
	for i := len(msgs) - 1; i >= 0; i-- {
		mm, ok := msgs[i].(map[string]interface{})
		if !ok {
			continue
		}
		content, ok := mm["content"]
		if !ok {
			continue
		}
		raw, err := json.Marshal(content)
		if err != nil {
			continue
		}
		// 字符串形式无图片
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			continue
		}
		var blocks []map[string]interface{}
		if err := json.Unmarshal(raw, &blocks); err != nil {
			continue
		}
		hasImg := false
		for _, b := range blocks {
			if t, _ := b["type"].(string); t == "image" {
				hasImg = true
				break
			}
		}
		if !hasImg {
			continue
		}
		isLatest := !latestMsgDone
		latestMsgDone = true
		changed := false
		var newBlocks []map[string]interface{}
		for _, b := range blocks {
			typ, _ := b["type"].(string)
			if typ == "image" {
				if isLatest {
					desc := descAt(descriptions, imgIdx)
					imgIdx++
					newBlocks = append(newBlocks, map[string]interface{}{"type": "text", "text": desc})
				} else {
					newBlocks = append(newBlocks, map[string]interface{}{"type": "text", "text": historyImagePlaceholder})
				}
				changed = true
			} else {
				newBlocks = append(newBlocks, b)
			}
		}
		if changed {
			if b, err := json.Marshal(newBlocks); err == nil {
				mm["content"] = json.RawMessage(b)
				msgs[i] = mm
			}
		}
	}
	parsed["messages"] = msgs
}

// ===== Responses 协议 =====

type responsesInputImageBlock struct {
	Type     string `json:"type"` // input_image
	ImageURL string `json:"image_url"`
	FileID   string `json:"file_id,omitempty"`
}

func extractImagesResponses(body []byte) []ImageRef {
	var req struct {
		Input []struct {
			Content json.RawMessage `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil
	}
	var refs []ImageRef
	for _, item := range req.Input {
		refs = append(refs, extractResponsesContentImages(item.Content)...)
	}
	return refs
}

// extractLatestUserImagesResponses 提取【本轮新增】的图片（带图 user 后无 assistant 回复才触发）
// 注意：Responses API 的 input item 可省略 role（缺省 = user），
// 且 type 为 "message" 或缺失时也视为用户输入。
// 只有显式 type="function_call_output" / "function_call" 不是 user。
func extractLatestUserImagesResponses(body []byte) []ImageRef {
	var req struct {
		Input []struct {
			Role    string          `json:"role"`
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil
	}
	for i := len(req.Input) - 1; i >= 0; i-- {
		item := req.Input[i]
		// role 显式指定时按 role 判断；省略 role 时 type=message/缺省视为 user
		isUser := item.Role == "user" ||
			(item.Role == "" && item.Type != "function_call_output" && item.Type != "function_call")
		if !isUser {
			continue
		}
		refs := extractResponsesContentImages(item.Content)
		if len(refs) == 0 {
			continue
		}
		for j := i + 1; j < len(req.Input); j++ {
			it := req.Input[j]
			if it.Role == "assistant" || it.Type == "message" || it.Type == "" {
				return nil
			}
		}
		return refs
	}
	return nil
}

func extractResponsesContentImages(content json.RawMessage) []ImageRef {
	// content 可为字符串或块数组
	var str string
	if err := json.Unmarshal(content, &str); err == nil {
		return nil
	}
	var blocks []responsesInputImageBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}
	var refs []ImageRef
	for _, b := range blocks {
		if b.Type == "input_image" {
			if b.ImageURL != "" {
				refs = append(refs, ImageRef{URL: b.ImageURL, usableURL: b.ImageURL})
			} else if b.FileID != "" {
				// file_id 引用无法直接识图，跳过（保留原块，由主模型处理）
				continue
			}
		}
	}
	return refs
}

func replaceResponsesImages(parsed map[string]interface{}, descriptions []string) {
	input, ok := parsed["input"].([]interface{})
	if !ok {
		return
	}
	// 从后往前：第一条【含图】input 的所有图片分配描述，更早历史图片分配占位
	imgIdx := 0
	latestMsgDone := false
	for i := len(input) - 1; i >= 0; i-- {
		im, ok := input[i].(map[string]interface{})
		if !ok {
			continue
		}
		content, ok := im["content"]
		if !ok {
			continue
		}
		raw, err := json.Marshal(content)
		if err != nil {
			continue
		}
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			continue
		}
		var blocks []map[string]interface{}
		if err := json.Unmarshal(raw, &blocks); err != nil {
			continue
		}
		hasImg := false
		for _, b := range blocks {
			if t, _ := b["type"].(string); t == "input_image" {
				hasImg = true
				break
			}
		}
		if !hasImg {
			continue
		}
		isLatest := !latestMsgDone
		latestMsgDone = true
		changed := false
		var newBlocks []map[string]interface{}
		for _, b := range blocks {
			typ, _ := b["type"].(string)
			if typ == "input_image" {
				if isLatest {
					desc := descAt(descriptions, imgIdx)
					imgIdx++
					newBlocks = append(newBlocks, map[string]interface{}{"type": "input_text", "text": desc})
				} else {
					newBlocks = append(newBlocks, map[string]interface{}{"type": "input_text", "text": historyImagePlaceholder})
				}
				changed = true
			} else {
				newBlocks = append(newBlocks, b)
			}
		}
		if changed {
			if b, err := json.Marshal(newBlocks); err == nil {
				im["content"] = json.RawMessage(b)
				input[i] = im
			}
		}
	}
	parsed["input"] = input
}

// descAt 获取第 idx 张图片的描述（越界时给出通用占位）
// 注入格式包含明确的指令：视觉内容已由视觉模型识别，主模型不应再尝试读取图片文件或调用工具看图
// imageURLValue 提取 image_url 字段的值（兼容对象 {"url":"..."} 或字符串 "https://..."）
func imageURLValue(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var urlStr string
	if err := json.Unmarshal(raw, &urlStr); err == nil {
		return urlStr
	}
	var urlObj struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &urlObj); err == nil {
		return urlObj.URL
	}
	return ""
}

// rawOf 从 map 中提取指定 key 的 JSON 原始字节
func rawOf(m map[string]interface{}, key string) json.RawMessage {
	v, ok := m[key]
	if !ok {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func descAt(descriptions []string, idx int) string {
	label := fmt.Sprintf("[图片%d]", idx+1)
	if idx < len(descriptions) {
		return fmt.Sprintf("%s 视觉内容（由视觉模型识别，以下内容即图片的全部视觉信息）:\n%s", label, descriptions[idx])
	}
	// 越界（描述为空，如无图分支调用 ReplaceImages(nil)）：统一用历史占位文本
	return historyImagePlaceholder
}

// VisionInstruction 图片替换时注入给主模型的系统指令（附在首条替换描述前）
// 明确告知主模型：视觉内容已完整提供，不要尝试读取图片文件、不要调用工具获取图片内容
const VisionInstruction = "注意：用户消息中的图片已由视觉模型识别，视觉内容已以文字形式完整提供（见[图片N]块）。" +
	"请直接基于提供的文字描述回答，不要尝试读取任何图片文件，不要调用工具（如 bash/read 文件）去获取图片内容——" +
	"原始图片文件对主模型不可访问，所有视觉识别工作已由专门模型完成。"

// VisionFailureInstruction 识图失败时注入给主模型的指令
// 明确告知主模型：不要尝试其他手段识图（本地 OCR/读文件/调工具），直接如实告知用户失败
const VisionFailureInstruction = "注意：用户消息中的图片识别失败（视觉模型未能处理）。" +
	"请直接告知用户图片识别失败，不要尝试任何其他方式读取或识别图片（不要调用 bash/读文件/本地 OCR 等工具），" +
	"原始图片文件对主模型不可访问。"

// extractImageRefsToUsable 提取所有可识图引用的 URL 列表（供识图请求构造）
func extractImageRefsToUsable(refs []ImageRef) []string {
	var urls []string
	for _, r := range refs {
		if u := r.UsableURL(); u != "" {
			urls = append(urls, u)
		}
	}
	return urls
}

// JoinDescriptions 将多张图片描述合并为一条文本（用于无数组场景）
func JoinDescriptions(descriptions []string) string {
	return strings.Join(descriptions, "\n")
}
