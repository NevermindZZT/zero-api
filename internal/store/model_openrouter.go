package store

import (
	"encoding/json"
	"fmt"
	"strings"
)

// OpenRouterModelInfo contains only model metadata that may be imported from OpenRouter.
type OpenRouterModelInfo struct {
	DisplayName      string
	ContextWindow    int
	MaxOutputTokens  int
	SupportsVision   bool
	SupportsThinking bool
	SupportsTools    bool
	Capabilities     []string
	InputModalities  []string
	OutputModalities []string
}

type OpenRouterModelUpdate struct {
	ModelID           int64
	Info              OpenRouterModelInfo
	PricingInput      float64
	PricingOutput     float64
	PricingCacheRead  float64
	PricingCacheWrite float64
	Fields            []string
	IncludePricing    bool
}

// ApplyOpenRouterUpdates updates selected model records atomically.
// Channel-specific fields such as channel_id, status, protocols and protocol URLs are never changed.
func (r *ModelRepo) ApplyOpenRouterUpdates(updates []OpenRouterModelUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("开始模型更新事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, update := range updates {
		if update.ModelID <= 0 {
			return fmt.Errorf("无效的模型 ID: %d", update.ModelID)
		}
		sets := make([]string, 0, len(update.Fields)+2)
		args := make([]interface{}, 0, len(update.Fields)+2)
		for _, field := range update.Fields {
			switch strings.TrimSpace(field) {
			case "display_name":
				sets = append(sets, "display_name = ?")
				args = append(args, update.Info.DisplayName)
			case "context_window":
				sets = append(sets, "context_window = ?")
				args = append(args, update.Info.ContextWindow)
			case "max_output_tokens":
				sets = append(sets, "max_output_tokens = ?")
				args = append(args, update.Info.MaxOutputTokens)
			case "supports_vision":
				sets = append(sets, "supports_vision = ?")
				args = append(args, update.Info.SupportsVision)
			case "supports_thinking":
				sets = append(sets, "supports_thinking = ?")
				args = append(args, update.Info.SupportsThinking)
			case "supports_tools":
				sets = append(sets, "supports_tools = ?")
				args = append(args, update.Info.SupportsTools)
			case "capabilities":
				encoded, marshalErr := json.Marshal(update.Info.Capabilities)
				if marshalErr != nil {
					return marshalErr
				}
				sets = append(sets, "capabilities = ?")
				args = append(args, encoded)
			case "input_modalities":
				encoded, marshalErr := json.Marshal(update.Info.InputModalities)
				if marshalErr != nil {
					return marshalErr
				}
				sets = append(sets, "input_modalities = ?")
				args = append(args, encoded)
			case "output_modalities":
				encoded, marshalErr := json.Marshal(update.Info.OutputModalities)
				if marshalErr != nil {
					return marshalErr
				}
				sets = append(sets, "output_modalities = ?")
				args = append(args, encoded)
			case "pricing_input":
				if update.IncludePricing {
					sets = append(sets, "pricing_input = ?")
					args = append(args, update.PricingInput)
				}
			case "pricing_output":
				if update.IncludePricing {
					sets = append(sets, "pricing_output = ?")
					args = append(args, update.PricingOutput)
				}
			case "pricing_cache_read":
				if update.IncludePricing {
					sets = append(sets, "pricing_cache_read = ?")
					args = append(args, update.PricingCacheRead)
				}
			case "pricing_cache_write":
				if update.IncludePricing {
					sets = append(sets, "pricing_cache_write = ?")
					args = append(args, update.PricingCacheWrite)
				}
			}
		}
		if len(sets) == 0 {
			continue
		}
		sets = append(sets, "user_modified = 1", "updated_at = CURRENT_TIMESTAMP")
		args = append(args, update.ModelID)
		result, execErr := tx.Exec("UPDATE models SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
		if execErr != nil {
			return fmt.Errorf("更新模型 %d 失败: %w", update.ModelID, execErr)
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
			if affectedErr != nil {
				return fmt.Errorf("检查模型 %d 更新结果失败: %w", update.ModelID, affectedErr)
			}
			return fmt.Errorf("模型 %d 不存在", update.ModelID)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交模型更新事务失败: %w", err)
	}
	return nil
}
