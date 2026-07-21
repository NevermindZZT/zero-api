package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/never/zero-api/internal/pricing"
)

// 模型列表缓存
var (
	modelCacheMu     sync.RWMutex
	modelCache       []Model
	modelCacheExpiry time.Time
)

// InvalidateModelCache 清除模型列表缓存（同步/编辑模型时调用）
func (r *ModelRepo) InvalidateModelCache() {
	modelCacheMu.Lock()
	defer modelCacheMu.Unlock()
	modelCache = nil
	modelCacheExpiry = time.Time{}
}

// Model 模型
type Model struct {
	ID              int64     `json:"id"`
	ChannelID       int64     `json:"channel_id"`
	ModelID         string    `json:"model_id"`
	DisplayName     string    `json:"display_name"`
	ContextWindow   int       `json:"context_window"`
	MaxOutputTokens int       `json:"max_output_tokens"`
	SupportsVision  bool      `json:"supports_vision"`
	SupportsThinking bool     `json:"supports_thinking"`
	SupportsTools   bool      `json:"supports_tools"`
	PricingInput      float64   `json:"pricing_input"`        // $/1M tokens（输入）
	PricingOutput     float64   `json:"pricing_output"`       // $/1M tokens（输出）
	PricingCacheRead  float64   `json:"pricing_cache_read"`   // $/1M tokens（缓存读取）
	PricingCacheWrite float64   `json:"pricing_cache_write"`  // $/1M tokens（缓存写入）
	PricingRules      string    `json:"pricing_rules"`        // 定价规则 JSON
	Status            string    `json:"status"` // active, inactive
	UserModified      bool      `json:"user_modified"`        // 用户是否手动编辑过
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	// 关联字段（查询时填充）
	ChannelName     string `json:"channel_name,omitempty"`
	ChannelType     string `json:"channel_type,omitempty"`
	ChannelPriority int    `json:"channel_priority,omitempty"`
	ChannelStatus   string `json:"channel_status,omitempty"`
}

// ParsedPricingRules 解析并返回定价规则列表
func (m *Model) ParsedPricingRules() pricing.PricingRules {
	return pricing.MustParsePricingRules(m.PricingRules)
}

type ModelRepo struct {
	db *DB
}

func NewModelRepo(db *DB) *ModelRepo {
	return &ModelRepo{db: db}
}

func (r *ModelRepo) List(channelID int64) ([]Model, error) {
	// 无筛选条件时使用缓存
	if channelID == 0 {
		modelCacheMu.RLock()
		if modelCache != nil && time.Now().Before(modelCacheExpiry) {
			result := make([]Model, len(modelCache))
			copy(result, modelCache)
			modelCacheMu.RUnlock()
			return result, nil
		}
		modelCacheMu.RUnlock()
	}

	var rows interface{ Scan(...interface{}) error; Close() error; Next() bool; Err() error }
	var err error

	userModifiedField := "m.user_modified"
	channelNameType := "COALESCE(c.name, ''), COALESCE(c.type, '')"
	channelPriority := "COALESCE(c.priority, 99)"
	channelStatus := "COALESCE(c.status, 'active')"
	if channelID > 0 {
		rows, err = r.db.Query(
			`SELECT m.id, m.channel_id, m.model_id, m.display_name,
			        m.context_window, m.max_output_tokens,
			        m.supports_vision, m.supports_thinking, m.supports_tools,
		        m.pricing_input, m.pricing_output, m.pricing_cache_read, m.pricing_cache_write,
		        m.pricing_rules,
		        m.status,`+userModifiedField+`, m.created_at, m.updated_at,
		        `+channelNameType+`, `+channelPriority+`, `+channelStatus+`
			 FROM models m LEFT JOIN channels c ON m.channel_id = c.id
			 WHERE m.channel_id = ? ORDER BY c.priority, m.id`, channelID)
	} else {
		rows, err = r.db.Query(
			`SELECT m.id, m.channel_id, m.model_id, m.display_name,
		        m.context_window, m.max_output_tokens,
		        m.supports_vision, m.supports_thinking, m.supports_tools,
		        m.pricing_input, m.pricing_output, m.pricing_cache_read, m.pricing_cache_write,
		        m.pricing_rules,
		        m.status,`+userModifiedField+`, m.created_at, m.updated_at,
		        `+channelNameType+`, `+channelPriority+`, `+channelStatus+`
			 FROM models m LEFT JOIN channels c ON m.channel_id = c.id
			 ORDER BY c.priority, m.id`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []Model
	for rows.Next() {
		var m Model
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.ModelID, &m.DisplayName,
			&m.ContextWindow, &m.MaxOutputTokens,
			&m.SupportsVision, &m.SupportsThinking, &m.SupportsTools,
			&m.PricingInput, &m.PricingOutput, &m.PricingCacheRead, &m.PricingCacheWrite,
			&m.PricingRules,
			&m.Status, &m.UserModified, &m.CreatedAt, &m.UpdatedAt,
			&m.ChannelName, &m.ChannelType, &m.ChannelPriority, &m.ChannelStatus); err != nil {
			return nil, err
		}
		models = append(models, m)
	}

	// channelID == 0 时写入缓存（5 分钟 TTL）
	if channelID == 0 && err == nil {
		modelCacheMu.Lock()
		modelCache = models
		modelCacheExpiry = time.Now().Add(5 * time.Minute)
		modelCacheMu.Unlock()
	}

	return models, nil
}

func (r *ModelRepo) GetByID(id int64) (*Model, error) {
	m := &Model{}
	err := r.db.QueryRow(
		`SELECT m.id, m.channel_id, m.model_id, m.display_name,
		        m.context_window, m.max_output_tokens,
		        m.supports_vision, m.supports_thinking, m.supports_tools,
		        m.pricing_input, m.pricing_output, m.pricing_cache_read, m.pricing_cache_write,
		        m.pricing_rules,
		        m.status, m.user_modified, m.created_at, m.updated_at,
		        COALESCE(c.name, ''), COALESCE(c.type, ''), COALESCE(c.priority, 99), COALESCE(c.status, 'active')
		 FROM models m LEFT JOIN channels c ON m.channel_id = c.id
		 WHERE m.id = ?`, id,
	).Scan(&m.ID, &m.ChannelID, &m.ModelID, &m.DisplayName,
		&m.ContextWindow, &m.MaxOutputTokens,
		&m.SupportsVision, &m.SupportsThinking, &m.SupportsTools,
		&m.PricingInput, &m.PricingOutput, &m.PricingCacheRead, &m.PricingCacheWrite,
		&m.PricingRules,
		&m.Status, &m.UserModified, &m.CreatedAt, &m.UpdatedAt,
		&m.ChannelName, &m.ChannelType, &m.ChannelPriority, &m.ChannelStatus)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (r *ModelRepo) Upsert(m *Model) (int64, error) {
	// INSERT 时设置完整元信息，ON CONFLICT 仅更新 display_name（如原值等于 model_id 说明未手动修改过）
	// 其他字段（context_window / pricing / supports_*）保护手动编辑不被同步覆盖
	// 如需刷新元信息，请删除模型后重新同步
	result, err := r.db.Exec(
		`INSERT INTO models (channel_id, model_id, display_name, context_window, max_output_tokens,
		                     supports_vision, supports_thinking, supports_tools,
		                     pricing_input, pricing_output, pricing_cache_read, pricing_cache_write, pricing_rules, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id, model_id) DO UPDATE SET
		   display_name = CASE WHEN display_name = model_id THEN ? ELSE display_name END,
		   context_window = CASE WHEN user_modified = 0 THEN ? ELSE context_window END,
		   max_output_tokens = CASE WHEN user_modified = 0 THEN ? ELSE max_output_tokens END,
		   supports_vision = CASE WHEN user_modified = 0 THEN ? ELSE supports_vision END,
		   supports_thinking = CASE WHEN user_modified = 0 THEN ? ELSE supports_thinking END,
		   supports_tools = CASE WHEN user_modified = 0 THEN ? ELSE supports_tools END,
		   pricing_input = CASE WHEN user_modified = 0 THEN ? ELSE pricing_input END,
		   pricing_output = CASE WHEN user_modified = 0 THEN ? ELSE pricing_output END,
		   pricing_cache_read = CASE WHEN user_modified = 0 THEN ? ELSE pricing_cache_read END,
		   pricing_cache_write = CASE WHEN user_modified = 0 THEN ? ELSE pricing_cache_write END,
		   pricing_rules = CASE WHEN user_modified = 0 THEN ? ELSE pricing_rules END,
		   updated_at = CURRENT_TIMESTAMP`,
		m.ChannelID, m.ModelID, m.DisplayName, m.ContextWindow, m.MaxOutputTokens,
		m.SupportsVision, m.SupportsThinking, m.SupportsTools,
		m.PricingInput, m.PricingOutput, m.PricingCacheRead, m.PricingCacheWrite, m.PricingRules, m.Status,
		m.DisplayName,
		m.ContextWindow, m.MaxOutputTokens,
		m.SupportsVision, m.SupportsThinking, m.SupportsTools,
		m.PricingInput, m.PricingOutput, m.PricingCacheRead, m.PricingCacheWrite,
		m.PricingRules,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *ModelRepo) Update(m *Model) error {
	_, err := r.db.Exec(
		`UPDATE models SET display_name=?, context_window=?, max_output_tokens=?,
		 supports_vision=?, supports_thinking=?, supports_tools=?,
		 pricing_input=?, pricing_output=?, pricing_cache_read=?, pricing_cache_write=?,
		 pricing_rules=?,
		 status=?, user_modified=1, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		m.DisplayName, m.ContextWindow, m.MaxOutputTokens,
		m.SupportsVision, m.SupportsThinking, m.SupportsTools,
		m.PricingInput, m.PricingOutput, m.PricingCacheRead, m.PricingCacheWrite,
		m.PricingRules,
		m.Status, m.ID,
	)
	return err
}

// ClearUserModified 清除 user_modified 标记
func (r *ModelRepo) ClearUserModified(id int64) error {
	_, err := r.db.Exec(`UPDATE models SET user_modified = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// SetUserModified 设置 user_modified 标记
func (r *ModelRepo) SetUserModified(id int64) error {
	_, err := r.db.Exec(`UPDATE models SET user_modified = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

func (r *ModelRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM models WHERE id = ?`, id)
	return err
}

// ToggleStatus 切换模型启用/禁用状态（不标记 user_modified，不影响同步覆盖）
func (r *ModelRepo) ToggleStatus(id int64) error {
	_, err := r.db.Exec(`UPDATE models SET status = CASE WHEN status = 'active' THEN 'inactive' ELSE 'active' END, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// BatchSetStatus 批量设置模型状态
func (r *ModelRepo) BatchSetStatus(ids []int64, status string) error {
	for _, id := range ids {
		if _, err := r.db.Exec(`UPDATE models SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, id); err != nil {
			return err
		}
	}
	return nil
}

// BatchDelete 批量删除模型
func (r *ModelRepo) BatchDelete(ids []int64) error {
	for _, id := range ids {
		if _, err := r.db.Exec(`DELETE FROM models WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// ModelExportItem 导出/导入用的模型数据结构（只含关键字段，不含 DB 内部 id/channel_id）
type ModelExportItem struct {
	ModelID           string          `json:"model_id"`
	DisplayName       string          `json:"display_name,omitempty"`
	ContextWindow     int             `json:"context_window,omitempty"`
	MaxOutputTokens   int             `json:"max_output_tokens,omitempty"`
	SupportsVision    bool            `json:"supports_vision,omitempty"`
	SupportsThinking  bool            `json:"supports_thinking,omitempty"`
	SupportsTools     bool            `json:"supports_tools,omitempty"`
	PricingInput      float64         `json:"pricing_input,omitempty"`
	PricingOutput     float64         `json:"pricing_output,omitempty"`
	PricingCacheRead  float64         `json:"pricing_cache_read,omitempty"`
	PricingCacheWrite float64         `json:"pricing_cache_write,omitempty"`
	PricingRules      json.RawMessage `json:"pricing_rules,omitempty"`
	Status            string          `json:"status,omitempty"`
}

// BatchEditFields 批量编辑的可选字段
type BatchEditFields struct {
	PricingInput      *float64
	PricingOutput     *float64
	PricingCacheRead  *float64
	PricingCacheWrite *float64
	ContextWindow     *int
	MaxOutputTokens   *int
	SupportsVision    *bool
	SupportsThinking  *bool
	SupportsTools     *bool
}

// ExportJSON 导出所有模型为 JSON
func (r *ModelRepo) ExportJSON() ([]byte, error) {
	models, err := r.List(0)
	if err != nil {
		return nil, err
	}
	items := make([]ModelExportItem, 0, len(models))
	for _, m := range models {
		pr := json.RawMessage("[]")
		if m.PricingRules != "" && m.PricingRules != "[]" {
			pr = json.RawMessage(m.PricingRules)
		}
		items = append(items, ModelExportItem{
			ModelID:           m.ModelID,
			DisplayName:       m.DisplayName,
			ContextWindow:     m.ContextWindow,
			MaxOutputTokens:   m.MaxOutputTokens,
			SupportsVision:    m.SupportsVision,
			SupportsThinking:  m.SupportsThinking,
			SupportsTools:     m.SupportsTools,
			PricingInput:      m.PricingInput,
			PricingOutput:     m.PricingOutput,
			PricingCacheRead:  m.PricingCacheRead,
			PricingCacheWrite: m.PricingCacheWrite,
			PricingRules:      pr,
			Status:            m.Status,
		})
	}
	type wrapper struct {
		Version    int               `json:"version"`
		ExportedAt string            `json:"exported_at"`
		Models     []ModelExportItem `json:"models"`
	}
	return json.MarshalIndent(wrapper{Version: 1, ExportedAt: time.Now().UTC().Format(time.RFC3339), Models: items}, "", "  ")
}

// ImportJSON 从导出数据批量导入（按 model_id 匹配任意渠道，覆盖并标记 user_modified=1）
func (r *ModelRepo) ImportJSON(items []ModelExportItem, overwriteUserModified bool) (int, error) {
	count := 0
	for _, item := range items {
		if item.ModelID == "" {
			continue
		}
		pr := "[]"
		if len(item.PricingRules) > 0 {
			pr = string(item.PricingRules)
		}
		status := item.Status
		if status == "" {
			status = "active"
		}
		// 按 model_id 匹配任意记录
		existing, _ := r.findByModelID(item.ModelID)
		if existing != nil {
			if existing.UserModified && !overwriteUserModified {
				continue // 跳过用户已手动编辑的模型
			}
			existing.DisplayName = item.DisplayName
			existing.ContextWindow = item.ContextWindow
			existing.MaxOutputTokens = item.MaxOutputTokens
			existing.SupportsVision = item.SupportsVision
			existing.SupportsThinking = item.SupportsThinking
			existing.SupportsTools = item.SupportsTools
			existing.PricingInput = item.PricingInput
			existing.PricingOutput = item.PricingOutput
			existing.PricingCacheRead = item.PricingCacheRead
			existing.PricingCacheWrite = item.PricingCacheWrite
			existing.PricingRules = pr
			existing.Status = status
			existing.UserModified = true
			if err := r.Update(existing); err != nil {
				return count, fmt.Errorf("更新模型 %s 失败: %w", item.ModelID, err)
			}
		}
		count++
	}
	return count, nil
}

// findByModelID 按 model_id 查找（返回第一条匹配记录）
func (r *ModelRepo) findByModelID(modelID string) (*Model, error) {
	m := &Model{}
	err := r.db.QueryRow(
		`SELECT id, channel_id, model_id, display_name, context_window, max_output_tokens,
		        supports_vision, supports_thinking, supports_tools,
		        pricing_input, pricing_output, pricing_cache_read, pricing_cache_write,
		        pricing_rules,
		        status, user_modified, created_at, updated_at
		 FROM models WHERE model_id = ? LIMIT 1`, modelID,
	).Scan(&m.ID, &m.ChannelID, &m.ModelID, &m.DisplayName,
		&m.ContextWindow, &m.MaxOutputTokens,
		&m.SupportsVision, &m.SupportsThinking, &m.SupportsTools,
		&m.PricingInput, &m.PricingOutput, &m.PricingCacheRead, &m.PricingCacheWrite,
		&m.PricingRules,
		&m.Status, &m.UserModified, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// BatchEdit 批量编辑模型（仅更新非 nil 字段，标记 user_modified=1）
func (r *ModelRepo) BatchEdit(ids []int64, fields BatchEditFields) error {
	setClauses := []string{}
	args := []interface{}{}

	if fields.PricingInput != nil {
		setClauses = append(setClauses, "pricing_input = ?")
		args = append(args, *fields.PricingInput)
	}
	if fields.PricingOutput != nil {
		setClauses = append(setClauses, "pricing_output = ?")
		args = append(args, *fields.PricingOutput)
	}
	if fields.PricingCacheRead != nil {
		setClauses = append(setClauses, "pricing_cache_read = ?")
		args = append(args, *fields.PricingCacheRead)
	}
	if fields.PricingCacheWrite != nil {
		setClauses = append(setClauses, "pricing_cache_write = ?")
		args = append(args, *fields.PricingCacheWrite)
	}
	if fields.ContextWindow != nil {
		setClauses = append(setClauses, "context_window = ?")
		args = append(args, *fields.ContextWindow)
	}
	if fields.MaxOutputTokens != nil {
		setClauses = append(setClauses, "max_output_tokens = ?")
		args = append(args, *fields.MaxOutputTokens)
	}
	if fields.SupportsVision != nil {
		setClauses = append(setClauses, "supports_vision = ?")
		args = append(args, *fields.SupportsVision)
	}
	if fields.SupportsThinking != nil {
		setClauses = append(setClauses, "supports_thinking = ?")
		args = append(args, *fields.SupportsThinking)
	}
	if fields.SupportsTools != nil {
		setClauses = append(setClauses, "supports_tools = ?")
		args = append(args, *fields.SupportsTools)
	}
	if len(setClauses) == 0 {
		return nil
	}

	setClauses = append(setClauses, "user_modified = 1", "updated_at = CURRENT_TIMESTAMP")
	setSQL := strings.Join(setClauses, ", ")

	for _, id := range ids {
		allArgs := append([]interface{}{}, args...)
		allArgs = append(allArgs, id)
		if _, err := r.db.Exec("UPDATE models SET "+setSQL+" WHERE id = ?", allArgs...); err != nil {
			return fmt.Errorf("批量编辑模型 %d 失败: %w", id, err)
		}
	}
	return nil
}

// BatchClearUserModified 批量清除 user_modified 标记（允许同步覆盖）
func (r *ModelRepo) BatchClearUserModified(ids []int64) error {
	for _, id := range ids {
		if err := r.ClearUserModified(id); err != nil {
			return err
		}
	}
	return nil
}

// FindByChannelAndModelID 根据渠道ID和模型ID查找
func (r *ModelRepo) FindByChannelAndModelID(channelID int64, modelID string) (*Model, error) {
	m := &Model{}
	err := r.db.QueryRow(
		`SELECT id, channel_id, model_id, display_name, context_window, max_output_tokens,
		        supports_vision, supports_thinking, supports_tools,
		        pricing_input, pricing_output, pricing_cache_read, pricing_cache_write,
		        pricing_rules,
		        status, user_modified, created_at, updated_at
		 FROM models WHERE channel_id = ? AND model_id = ?`, channelID, modelID,
	).Scan(&m.ID, &m.ChannelID, &m.ModelID, &m.DisplayName,
		&m.ContextWindow, &m.MaxOutputTokens,
		&m.SupportsVision, &m.SupportsThinking, &m.SupportsTools,
		&m.PricingInput, &m.PricingOutput, &m.PricingCacheRead, &m.PricingCacheWrite,
		&m.PricingRules,
		&m.Status, &m.UserModified, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return m, nil
}
