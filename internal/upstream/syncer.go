package upstream

import (
	"fmt"
	"io"
	"net/http"

	"github.com/never/zero-api/internal/adapter"
	"github.com/never/zero-api/internal/store"
)

// Syncer 负责从上游渠道同步模型信息
type Syncer struct {
	channelRepo *store.ChannelRepo
	modelRepo   *store.ModelRepo
}

func NewSyncer(channelRepo *store.ChannelRepo, modelRepo *store.ModelRepo) *Syncer {
	return &Syncer{channelRepo: channelRepo, modelRepo: modelRepo}
}

// SyncModels 同步指定渠道的模型列表
func (s *Syncer) SyncModels(channelID int64) (int, error) {
	ch, err := s.channelRepo.GetByID(channelID)
	if err != nil {
		return 0, fmt.Errorf("获取渠道失败: %w", err)
	}

	adapt := adapter.NewAdapter(ch.Type)

	// 获取上游模型列表
	url, headers := adapt.GetModelsURL(ch.BaseURL, ch.APIKey)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("构造请求失败: %w", err)
	}
	for k, v := range headers {
		req.Header[k] = v
	}

	resp, err := NewHTTPClient().Do(req)
	if err != nil {
		return 0, fmt.Errorf("请求上游失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	models, err := adapt.ParseModelsResponse(body)
	if err != nil {
		return 0, fmt.Errorf("解析模型列表失败: %w", err)
	}

	count := 0
	for _, um := range models {
		displayName := um.ID
		if um.Name != "" {
			displayName = um.Name
		}
		m := &store.Model{
			ChannelID:        channelID,
			ModelID:          um.ID,
			DisplayName:      displayName,
			ContextWindow:    um.ContextWindow,
			MaxOutputTokens:  um.MaxOutputTokens,
			SupportsVision:   um.SupportsVision,
			SupportsThinking: um.SupportsThinking,
			SupportsTools:    um.SupportsTools,
			Status:           "active",
		}
		if _, err := s.modelRepo.Upsert(m); err == nil {
			count++
		}
	}

	return count, nil
}
