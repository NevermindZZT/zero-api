package store

import (
	"sync"
	"time"
)

// 渠道缓存：GetByID 缓存，5 分钟 TTL
var (
	channelCacheMu     sync.RWMutex
	channelCache       = make(map[int64]*cachedChannel)
)

type cachedChannel struct {
	ch     *Channel
	expiry time.Time
}

// InvalidateChannelCache 清除渠道缓存（创建/更新/删除渠道时调用）
func (r *ChannelRepo) InvalidateChannelCache() {
	channelCacheMu.Lock()
	defer channelCacheMu.Unlock()
	channelCache = make(map[int64]*cachedChannel)
}

// Channel 渠道商
type Channel struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Type            string    `json:"type"`       // openai, anthropic, gemini, openrouter
	BaseURL         string    `json:"base_url"`
	APIKey          string    `json:"api_key,omitempty"`
	Status          string    `json:"status"` // active, inactive
	Priority        int       `json:"priority"`   // 0=最高优先级，越大优先级越低
	UseProxy        bool      `json:"use_proxy"`  // 是否通过全局出站代理转发请求
	FailoverEnabled bool      `json:"failover_enabled"` // 是否启用熔断回落
	TestModel       string    `json:"test_model"`       // 熔断探测用模型 ID
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ChannelRepo struct {
	db *DB
}

func NewChannelRepo(db *DB) *ChannelRepo {
	return &ChannelRepo{db: db}
}

func (r *ChannelRepo) List() ([]Channel, error) {
	rows, err := r.db.Query(`SELECT id, name, type, base_url, api_key, status, priority, use_proxy, failover_enabled, test_model, created_at, updated_at FROM channels ORDER BY priority, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.BaseURL, &c.APIKey, &c.Status, &c.Priority, &c.UseProxy, &c.FailoverEnabled, &c.TestModel, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, c)
	}
	return channels, nil
}

func (r *ChannelRepo) GetByID(id int64) (*Channel, error) {
	// 查缓存
	channelCacheMu.RLock()
	if cached, ok := channelCache[id]; ok && time.Now().Before(cached.expiry) {
		ch := *cached.ch
		channelCacheMu.RUnlock()
		return &ch, nil
	}
	channelCacheMu.RUnlock()

	c := &Channel{}
	err := r.db.QueryRow(
		`SELECT id, name, type, base_url, api_key, status, priority, use_proxy, failover_enabled, test_model, created_at, updated_at FROM channels WHERE id = ?`, id,
	).Scan(&c.ID, &c.Name, &c.Type, &c.BaseURL, &c.APIKey, &c.Status, &c.Priority, &c.UseProxy, &c.FailoverEnabled, &c.TestModel, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	// 写入缓存（5 分钟 TTL）
	channelCacheMu.Lock()
	channelCache[id] = &cachedChannel{ch: c, expiry: time.Now().Add(5 * time.Minute)}
	channelCacheMu.Unlock()
	return c, nil
}

func (r *ChannelRepo) Create(c *Channel) (int64, error) {
	result, err := r.db.Exec(
		`INSERT INTO channels (name, type, base_url, api_key, status, priority, use_proxy, failover_enabled, test_model) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.Type, c.BaseURL, c.APIKey, c.Status, c.Priority, c.UseProxy, c.FailoverEnabled, c.TestModel,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *ChannelRepo) Update(c *Channel) error {
	_, err := r.db.Exec(
		`UPDATE channels SET name=?, type=?, base_url=?, api_key=?, status=?, priority=?, use_proxy=?, failover_enabled=?, test_model=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		c.Name, c.Type, c.BaseURL, c.APIKey, c.Status, c.Priority, c.UseProxy, c.FailoverEnabled, c.TestModel, c.ID,
	)
	return err
}

func (r *ChannelRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM channels WHERE id = ?`, id)
	return err
}

// ToggleStatus 切换渠道启用/禁用状态
func (r *ChannelRepo) ToggleStatus(id int64) (*Channel, error) {
	ch, err := r.GetByID(id)
	if err != nil {
		return nil, err
	}
	newStatus := "inactive"
	if ch.Status != "active" {
		newStatus = "active"
	}
	_, err = r.db.Exec(`UPDATE channels SET status=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, newStatus, id)
	if err != nil {
		return nil, err
	}
	ch.Status = newStatus
	return ch, nil
}
