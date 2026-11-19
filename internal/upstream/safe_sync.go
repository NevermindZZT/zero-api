package upstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/never/zero-api/internal/adapter"
	"github.com/never/zero-api/internal/store"
)

const maxPreviewBody = 8 << 20
const maxPreviewModels = 10000
const maxPreviewPages = 50
const previewLifetime = 5 * time.Minute
const maxPreviews = 64

var ErrPreviewConflict = errors.New("预览已过期或数据已变更，请重新预览")

type PreviewModel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Existing bool   `json:"existing"`
}
type StaleModel struct {
	ModelID      string `json:"model_id"`
	DisplayName  string `json:"display_name"`
	UserModified bool   `json:"user_modified"`
}
type PreviewResult struct {
	PreviewID   string         `json:"preview_id"`
	Models      []PreviewModel `json:"models"`
	StaleModels []StaleModel   `json:"stale_models"`
}
type safeModel struct {
	adapter.ModelInfo
	fingerprint [32]byte
}

type syncPreview struct {
	channelID   int64
	fingerprint string
	upstream    []safeModel
	staleIDs    []int64
	expires     time.Time
}

// fetchCompleteModels treats model listings as authoritative only after strict
// validation. It never uses AnthropicAdapter's hard-coded response parser.
func fetchCompleteModels(ctx context.Context, ch *store.Channel) ([]safeModel, error) {
	if ch.Type != "openai" && ch.Type != "responses" && ch.Type != "anthropic" && ch.Type != "gemini" {
		return nil, fmt.Errorf("该渠道类型不支持安全模型清理")
	}
	adapt := adapter.NewAdapter(ch.Type)
	baseURL := strings.TrimRight(ch.BaseURL, "/")
	if ch.Type != "gemini" && strings.HasSuffix(baseURL, "/v1") {
		baseURL = strings.TrimSuffix(baseURL, "/v1")
	}
	rawURL, headers := adapt.GetModelsURL(baseURL, ch.APIKey)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("无效的上游模型地址")
	}
	// The legacy Gemini URL builder interpolates the key without escaping.
	if ch.Type == "gemini" && ch.APIKey != "" {
		u, err = url.Parse(strings.Split(rawURL, "?")[0])
		if err != nil {
			return nil, fmt.Errorf("无效的上游模型地址")
		}
		q := u.Query()
		q.Set("key", ch.APIKey)
		u.RawQuery = q.Encode()
	}
	client := NewHTTPClient()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	var all []safeModel
	seen, cursors := map[string]bool{}, map[string]bool{}
	for page := 0; page < maxPreviewPages; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("构造上游模型请求失败")
		}
		req.Header = headers.Clone()
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("请求上游模型列表失败")
		}
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Range") != "" || resp.Header.Get("Link") != "" || resp.Header.Get("X-Next-Page") != "" || resp.Header.Get("X-Next-Cursor") != "" || resp.Header.Get("X-Has-More") != "" {
			resp.Body.Close()
			return nil, fmt.Errorf("上游模型列表状态异常或分页不完整")
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxPreviewBody+1))
		resp.Body.Close()
		if err != nil || len(body) > maxPreviewBody {
			return nil, fmt.Errorf("上游模型列表读取失败或超过大小限制")
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
			return nil, fmt.Errorf("上游模型列表不是有效 JSON 对象")
		}
		for _, key := range []string{"error", "errors", "pagination", "paging", "page_info", "pageInfo"} {
			if raw, ok := obj[key]; ok && string(raw) != "null" && string(raw) != "[]" {
				return nil, fmt.Errorf("上游返回错误或无法验证的分页信息")
			}
		}
		// Unknown continuation signals are never ignored.
		for _, key := range []string{"next", "next_page", "nextPage", "next_page_token", "next_cursor", "nextCursor", "continuation_token", "continuationToken", "hasMore", "hasNextPage", "has_next_page", "has_next", "hasNext", "next_token", "nextToken", "next_page_url", "nextPageUrl", "total_pages", "totalPages", "page_count"} {
			if raw, ok := obj[key]; ok && string(raw) != "null" && string(raw) != `""` && string(raw) != "false" {
				return nil, fmt.Errorf("上游返回未支持的分页标记，无法确认完整列表")
			}
		}
		for _, key := range []string{"truncated", "incomplete", "is_truncated", "partial"} {
			if raw, ok := obj[key]; ok && string(raw) != "false" && string(raw) != "null" {
				return nil, fmt.Errorf("上游模型列表可能不完整")
			}
		}
		field := "data"
		if ch.Type == "gemini" {
			field = "models"
		}
		raw, ok := obj[field]
		if !ok || string(raw) == "null" {
			return nil, fmt.Errorf("上游未返回可验证的模型数组")
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
			return nil, fmt.Errorf("上游模型列表为空或不完整，禁止清理")
		}
		for _, item := range items {
			var m struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				DisplayName string `json:"display_name"`
			}
			if err := json.Unmarshal(item, &m); err != nil {
				return nil, fmt.Errorf("上游模型项格式无效")
			}
			id, name := m.ID, m.Name
			if ch.Type == "gemini" {
				if !strings.HasPrefix(m.Name, "models/") {
					return nil, fmt.Errorf("Gemini 模型名称无效")
				}
				id = strings.TrimPrefix(m.Name, "models/")
				name = m.DisplayName
			} else if ch.Type == "anthropic" {
				name = m.DisplayName
			}
			if id == "" || len(id) > 512 || strings.TrimSpace(id) != id || strings.IndexFunc(id, unicode.IsControl) >= 0 || seen[id] {
				return nil, fmt.Errorf("上游模型 ID 为空、无效或重复")
			}
			seen[id] = true
			var rawObject interface{}
			decoder := json.NewDecoder(bytes.NewReader(item))
			decoder.UseNumber()
			if err := decoder.Decode(&rawObject); err != nil {
				return nil, fmt.Errorf("上游模型项格式无效")
			}
			canonical, err := json.Marshal(rawObject)
			if err != nil {
				return nil, fmt.Errorf("上游模型项格式无效")
			}
			info := safeModel{ModelInfo: adapter.ModelInfo{ID: id, Name: name}, fingerprint: sha256.Sum256(canonical)}
			all = append(all, info)
			if len(all) > maxPreviewModels {
				return nil, fmt.Errorf("上游模型数量超过安全限制")
			}
		}
		// Verify advertised totals when present; they must agree after all pages.
		if ch.Type == "anthropic" {
			if _, ok := obj["has_more"]; !ok {
				return nil, fmt.Errorf("Anthropic 模型列表缺少分页状态，无法确认完整")
			}
		}
		var next string
		if raw, ok := obj["has_more"]; ok {
			if string(raw) != "true" && string(raw) != "false" {
				return nil, fmt.Errorf("上游分页字段无效")
			}
			var more bool
			if err := json.Unmarshal(raw, &more); err != nil {
				return nil, fmt.Errorf("上游分页字段无效")
			}
			if more {
				if ch.Type != "anthropic" {
					return nil, fmt.Errorf("上游分页不完整，暂不支持此分页方式")
				}
				if err := json.Unmarshal(obj["last_id"], &next); err != nil || next == "" || next != all[len(all)-1].ID {
					return nil, fmt.Errorf("上游分页缺少游标")
				}
				q := u.Query()
				q.Set("after_id", next)
				u.RawQuery = q.Encode()
			}
		}
		if raw, ok := obj["nextPageToken"]; ok {
			var token string
			if err := json.Unmarshal(raw, &token); err != nil {
				return nil, fmt.Errorf("上游分页字段无效")
			}
			if token != "" {
				if ch.Type != "gemini" || next != "" {
					return nil, fmt.Errorf("上游分页不完整")
				}
				next = token
				q := u.Query()
				q.Set("pageToken", token)
				u.RawQuery = q.Encode()
			}
		}
		if next == "" {
			for _, key := range []string{"total", "total_count", "totalCount"} {
				if raw, ok := obj[key]; ok && string(raw) != "null" {
					var n int
					if json.Unmarshal(raw, &n) != nil || n != len(all) {
						return nil, fmt.Errorf("上游模型总数不匹配，禁止清理")
					}
				}
			}
			sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
			return all, nil
		}
		if cursors[next] {
			return nil, fmt.Errorf("上游分页游标循环")
		}
		cursors[next] = true
	}
	return nil, fmt.Errorf("上游模型分页超过安全限制")
}

func (s *Syncer) PreviewModels(ctx context.Context, channelID int64) (*PreviewResult, error) {
	before, err := s.modelRepo.SyncFingerprint(channelID)
	if err != nil {
		return nil, fmt.Errorf("读取渠道状态失败")
	}
	ch, err := s.channelRepo.GetByIDFresh(channelID)
	if err != nil {
		return nil, fmt.Errorf("获取渠道失败")
	}
	upstream, err := fetchCompleteModels(ctx, ch)
	if err != nil {
		return nil, err
	}
	existing, err := s.modelRepo.List(channelID)
	if err != nil {
		return nil, fmt.Errorf("读取渠道模型失败")
	}
	after, err := s.modelRepo.SyncFingerprint(channelID)
	if err != nil || before != after {
		return nil, ErrPreviewConflict
	}
	byID := make(map[string]store.Model, len(existing))
	for _, m := range existing {
		byID[m.ModelID] = m
	}
	present := make(map[string]bool, len(upstream))
	result := &PreviewResult{Models: []PreviewModel{}, StaleModels: []StaleModel{}}
	for _, m := range upstream {
		present[m.ID] = true
		_, ok := byID[m.ID]
		name := m.Name
		if name == "" {
			name = m.ID
		}
		result.Models = append(result.Models, PreviewModel{m.ID, name, ok})
	}
	staleIDs := []int64{}
	for _, m := range existing {
		if !present[m.ModelID] {
			staleIDs = append(staleIDs, m.ID)
			result.StaleModels = append(result.StaleModels, StaleModel{m.ModelID, m.DisplayName, m.UserModified})
		}
	}
	bytes := make([]byte, 32)
	if _, err = rand.Read(bytes); err != nil {
		return nil, fmt.Errorf("创建预览失败")
	}
	result.PreviewID = hex.EncodeToString(bytes)
	now := time.Now()
	snap := &syncPreview{channelID: channelID, fingerprint: after, upstream: upstream, staleIDs: staleIDs, expires: now.Add(previewLifetime)}
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	if s.previews == nil {
		s.previews = make(map[string]*syncPreview)
	}
	for id, p := range s.previews {
		if !now.Before(p.expires) {
			delete(s.previews, id)
		}
	}
	if len(s.previews) >= maxPreviews {
		return nil, fmt.Errorf("预览数量已达上限，请稍后重试")
	}
	s.previews[result.PreviewID] = snap
	return result, nil
}

func (s *Syncer) ConfirmModels(ctx context.Context, channelID int64, previewID string, selectedIDs []string) (int, int, error) {
	s.previewMu.Lock()
	snap := s.previews[previewID]
	if snap != nil && snap.channelID == channelID {
		delete(s.previews, previewID)
	}
	s.previewMu.Unlock()
	if snap == nil || snap.channelID != channelID || !time.Now().Before(snap.expires) {
		return 0, 0, ErrPreviewConflict
	}
	selected := map[string]bool{}
	available := map[string]safeModel{}
	for _, m := range snap.upstream {
		available[m.ID] = m
	}
	for _, id := range selectedIDs {
		if _, ok := available[id]; !ok || selected[id] {
			return 0, 0, fmt.Errorf("选择的模型无效或重复")
		}
		selected[id] = true
	}
	current, err := s.modelRepo.SyncFingerprint(channelID)
	if err != nil || current != snap.fingerprint {
		return 0, 0, ErrPreviewConflict
	}
	ch, err := s.channelRepo.GetByIDFresh(channelID)
	if err != nil {
		return 0, 0, ErrPreviewConflict
	}
	latest, err := fetchCompleteModels(ctx, ch)
	if err != nil {
		return 0, 0, fmt.Errorf("%w：确认前重新检查上游失败（%v）", ErrPreviewConflict, err)
	}
	if !reflect.DeepEqual(latest, snap.upstream) {
		return 0, 0, ErrPreviewConflict
	}
	models := make([]*store.Model, 0, len(selected))
	defaults := s.modelPresets.GetAll()
	for _, um := range snap.upstream {
		if !selected[um.ID] {
			continue
		}
		merged := s.mergeModelInfo(um.ModelInfo)
		name := merged.ID
		if merged.Name != "" {
			name = merged.Name
		}
		m := &store.Model{ChannelID: channelID, ModelID: merged.ID, DisplayName: name, ContextWindow: merged.ContextWindow, MaxOutputTokens: merged.MaxOutputTokens, SupportsVision: merged.SupportsVision, SupportsThinking: merged.SupportsThinking, SupportsTools: merged.SupportsTools, Protocols: merged.Protocols, Capabilities: merged.Capabilities, InputModalities: merged.InputModalities, OutputModalities: merged.OutputModalities, Status: "active"}
		if conf, ok := defaults[merged.ID]; ok {
			m.PricingInput = conf.PricingInput
			m.PricingOutput = conf.PricingOutput
			m.PricingCacheRead = conf.PricingCacheRead
			m.PricingCacheWrite = conf.PricingCacheWrite
			if len(conf.PricingRules) > 0 {
				b, e := json.Marshal(conf.PricingRules)
				if e != nil {
					return 0, 0, e
				}
				m.PricingRules = string(b)
			}
		}
		models = append(models, m)
	}
	if !time.Now().Before(snap.expires) {
		return 0, 0, ErrPreviewConflict
	}
	deleted, err := s.modelRepo.ApplySync(channelID, snap.fingerprint, models, snap.staleIDs)
	if errors.Is(err, store.ErrSyncStateChanged) {
		return 0, 0, ErrPreviewConflict
	}
	if err != nil {
		return 0, 0, err
	}
	return len(models), deleted, nil
}
