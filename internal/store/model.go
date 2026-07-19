package store

import (
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
