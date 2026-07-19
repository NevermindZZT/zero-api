package upstream

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/never/zero-api/internal/adapter"
	"github.com/never/zero-api/internal/config"
	"github.com/never/zero-api/internal/store"
)

// Syncer 负责从上游渠道同步模型信息
type Syncer struct {
	channelRepo   *store.ChannelRepo
	modelRepo     *store.ModelRepo
	configDefaults map[string]config.ModelDefault
}

func NewSyncer(channelRepo *store.ChannelRepo, modelRepo *store.ModelRepo, configDefaults map[string]config.ModelDefault) *Syncer {
	return &Syncer{
		channelRepo:    channelRepo,
		modelRepo:      modelRepo,
		configDefaults: configDefaults,
	}
}

// mergeModelInfo 合并三个优先级的数据（优先级: modelDB < config默认值 < 上游API）
// 最终存储到 DB 后，用户手动编辑（user_modified=1）的数据不会被同步覆盖
func (s *Syncer) mergeModelInfo(upstreamModel adapter.ModelInfo) adapter.ModelInfo {
	result := upstreamModel

	// 从 modelDB 补全空白字段（优先级1：内置数据库）
	if dbInfo := adapter.GetModelDBInfo(upstreamModel.ID); dbInfo != nil {
		if result.Name == "" {
			result.Name = dbInfo.Name
		}
		if result.ContextWindow == 0 {
			result.ContextWindow = dbInfo.ContextWindow
		}
		if result.MaxOutputTokens == 0 {
			result.MaxOutputTokens = dbInfo.MaxOutputTokens
		}
		if !result.SupportsVision {
			result.SupportsVision = dbInfo.SupportsVision
		}
		if !result.SupportsThinking {
			result.SupportsThinking = dbInfo.SupportsThinking
		}
		if !result.SupportsTools {
			result.SupportsTools = dbInfo.SupportsTools
		}
	}

	// 从配置文件默认值覆盖（优先级2）
	if conf, ok := s.configDefaults[upstreamModel.ID]; ok {
		if conf.ContextWindow > 0 {
			result.ContextWindow = conf.ContextWindow
		}
		if conf.MaxOutputTokens > 0 {
			result.MaxOutputTokens = conf.MaxOutputTokens
		}
		if conf.SupportsVision {
			result.SupportsVision = true
		}
		if conf.SupportsThinking {
			result.SupportsThinking = true
		}
		if conf.SupportsTools {
			result.SupportsTools = true
		}
	}

	return result
}

// SyncModels 同步指定渠道的模型列表
// 合并优先级：内置 modelDB < 配置文件默认值 < 上游 API < 用户手动编辑（不会被覆盖）
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
		// 合并三个优先级的数据
		merged := s.mergeModelInfo(um)

		displayName := merged.ID
		if merged.Name != "" {
			displayName = merged.Name
		}
		m := &store.Model{
			ChannelID:        channelID,
			ModelID:          merged.ID,
			DisplayName:      displayName,
			ContextWindow:    merged.ContextWindow,
			MaxOutputTokens:  merged.MaxOutputTokens,
			SupportsVision:   merged.SupportsVision,
			SupportsThinking: merged.SupportsThinking,
			SupportsTools:    merged.SupportsTools,
			Status:           "active",
		}

		// 从配置文件默认值中获取定价（上游 API 通常不返回定价）
		if conf, ok := s.configDefaults[merged.ID]; ok {
			m.PricingInput = conf.PricingInput
			m.PricingOutput = conf.PricingOutput
			m.PricingCacheRead = conf.PricingCacheRead
			m.PricingCacheWrite = conf.PricingCacheWrite
			// 同步定价规则（如有）
			if len(conf.PricingRules) > 0 {
				if b, err := json.Marshal(conf.PricingRules); err == nil {
					m.PricingRules = string(b)
				}
			}
		}

		if _, err := s.modelRepo.Upsert(m); err == nil {
			count++
		} else {
			log.Printf("[同步] upsert 模型 %s 失败: %v", merged.ID, err)
		}
	}

	// 记录同步结果
	log.Printf("[同步] 渠道 %s (%d) 同步完成: %d 个模型", ch.Name, channelID, count)

	return count, nil
}
