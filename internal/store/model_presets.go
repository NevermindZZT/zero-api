package store

import (
	"encoding/json"
	"fmt"

	"github.com/never/zero-api/internal/config"
)

// ApplyPresets 将默认模型信息应用到未手动修改的已有模型。
// user_modified=1 的模型完全保留用户配置。
func (r *ModelRepo) ApplyPresets(presets map[string]config.ModelDefault) (int, error) {
	updated := 0
	for modelID, preset := range presets {
		rules := "[]"
		if len(preset.PricingRules) > 0 {
			data, err := json.Marshal(preset.PricingRules)
			if err != nil {
				return updated, fmt.Errorf("序列化模型 %s 定价规则失败: %w", modelID, err)
			}
			rules = string(data)
		}
		result, err := r.db.Exec(`
			UPDATE models SET
				context_window = CASE WHEN ? > 0 THEN ? ELSE context_window END,
				max_output_tokens = CASE WHEN ? > 0 THEN ? ELSE max_output_tokens END,
				supports_vision = CASE WHEN ? THEN 1 ELSE supports_vision END,
				supports_thinking = CASE WHEN ? THEN 1 ELSE supports_thinking END,
				supports_tools = CASE WHEN ? THEN 1 ELSE supports_tools END,
				pricing_input = CASE WHEN ? != 0 OR pricing_input = 0 THEN ? ELSE pricing_input END,
				pricing_output = CASE WHEN ? != 0 OR pricing_output = 0 THEN ? ELSE pricing_output END,
				pricing_cache_read = CASE WHEN ? != 0 OR pricing_cache_read = 0 THEN ? ELSE pricing_cache_read END,
				pricing_cache_write = CASE WHEN ? != 0 OR pricing_cache_write = 0 THEN ? ELSE pricing_cache_write END,
				pricing_rules = CASE WHEN ? != '[]' THEN ? ELSE pricing_rules END,
				updated_at = CURRENT_TIMESTAMP
			WHERE model_id = ? AND user_modified = 0`,
			preset.ContextWindow, preset.ContextWindow,
			preset.MaxOutputTokens, preset.MaxOutputTokens,
			preset.SupportsVision, preset.SupportsThinking, preset.SupportsTools,
			preset.PricingInput, preset.PricingInput,
			preset.PricingOutput, preset.PricingOutput,
			preset.PricingCacheRead, preset.PricingCacheRead,
			preset.PricingCacheWrite, preset.PricingCacheWrite,
			rules, rules, modelID,
		)
		if err != nil {
			return updated, fmt.Errorf("应用模型 %s 默认信息失败: %w", modelID, err)
		}
		if count, err := result.RowsAffected(); err == nil {
			updated += int(count)
		}
	}
	r.InvalidateModelCache()
	return updated, nil
}
