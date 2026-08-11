package router

import (
	"encoding/json"
	"fmt"
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

func extractOpenAIContentImages(content json.RawMessage) []ImageRef {
	if len(content) == 0 || string(content) == "null" {
		return nil
	}
	// 字符串形式无图片
	var str string
	if err := json.Unmarshal(content, &str); err == nil {
		return nil
	}
	var parts []openAIContentPart
	if err := json.Unmarshal(content, &parts); err != nil {
		return nil
	}
	var refs []ImageRef
	for _, p := range parts {
		if p.Type == "image_url" && p.ImageURL.URL != "" {
			refs = append(refs, ImageRef{URL: p.ImageURL.URL, usableURL: p.ImageURL.URL})
		}
	}
	return refs
}

func replaceOpenAIImages(parsed map[string]interface{}, descriptions []string) {
	msgs, ok := parsed["messages"].([]interface{})
	if !ok {
		return
	}
	imgIdx := 0
	for i, m := range msgs {
		mm, ok := m.(map[string]interface{})
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
		var parts []openAIContentPart
		if err := json.Unmarshal(raw, &parts); err != nil {
			continue
		}
		changed := false
		var newParts []openAIContentPart
		for _, p := range parts {
			if p.Type == "image_url" && p.ImageURL.URL != "" {
				desc := descAt(descriptions, imgIdx)
				imgIdx++
				newParts = append(newParts, openAIContentPart{Type: "text", Text: desc})
				changed = true
			} else {
				newParts = append(newParts, p)
			}
		}
		if changed {
			if b, err := json.Marshal(newParts); err == nil {
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
		// 字符串形式无图片
		var str string
		if err := json.Unmarshal(m.Content, &str); err == nil {
			continue
		}
		var blocks []anthropicImageBlock
		if err := json.Unmarshal(m.Content, &blocks); err != nil {
			continue
		}
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
	}
	return refs
}

func replaceAnthropicImages(parsed map[string]interface{}, descriptions []string) {
	msgs, ok := parsed["messages"].([]interface{})
	if !ok {
		return
	}
	imgIdx := 0
	for i, m := range msgs {
		mm, ok := m.(map[string]interface{})
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
		changed := false
		var newBlocks []map[string]interface{}
		for _, b := range blocks {
			typ, _ := b["type"].(string)
			if typ == "image" {
				desc := descAt(descriptions, imgIdx)
				imgIdx++
				newBlocks = append(newBlocks, map[string]interface{}{"type": "text", "text": desc})
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
		// content 可为字符串或块数组
		var str string
		if err := json.Unmarshal(item.Content, &str); err == nil {
			continue
		}
		var blocks []responsesInputImageBlock
		if err := json.Unmarshal(item.Content, &blocks); err != nil {
			continue
		}
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
	}
	return refs
}

func replaceResponsesImages(parsed map[string]interface{}, descriptions []string) {
	input, ok := parsed["input"].([]interface{})
	if !ok {
		return
	}
	imgIdx := 0
	for i, item := range input {
		im, ok := item.(map[string]interface{})
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
		changed := false
		var newBlocks []map[string]interface{}
		for _, b := range blocks {
			typ, _ := b["type"].(string)
			if typ == "input_image" {
				desc := descAt(descriptions, imgIdx)
				imgIdx++
				newBlocks = append(newBlocks, map[string]interface{}{"type": "input_text", "text": desc})
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
func descAt(descriptions []string, idx int) string {
	label := fmt.Sprintf("[图片%d]", idx+1)
	if idx < len(descriptions) {
		return fmt.Sprintf("%s 视觉内容（由视觉模型识别，以下内容即图片的全部视觉信息）:\n%s", label, descriptions[idx])
	}
	return fmt.Sprintf("%s 视觉内容无法识别", label)
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
