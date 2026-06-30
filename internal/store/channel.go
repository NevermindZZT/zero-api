package store

import "time"

// Channel 渠道商
type Channel struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`       // openai, anthropic, gemini, openrouter
	BaseURL   string    `json:"base_url"`
	APIKey    string    `json:"api_key,omitempty"`
	Status    string    `json:"status"` // active, inactive
	Priority  int       `json:"priority"`   // 0=最高优先级，越大优先级越低
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChannelRepo struct {
	db *DB
}

func NewChannelRepo(db *DB) *ChannelRepo {
	return &ChannelRepo{db: db}
}

func (r *ChannelRepo) List() ([]Channel, error) {
	rows, err := r.db.Query(`SELECT id, name, type, base_url, api_key, status, priority, created_at, updated_at FROM channels ORDER BY priority, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.BaseURL, &c.APIKey, &c.Status, &c.Priority, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, c)
	}
	return channels, nil
}

func (r *ChannelRepo) GetByID(id int64) (*Channel, error) {
	c := &Channel{}
	err := r.db.QueryRow(
		`SELECT id, name, type, base_url, api_key, status, priority, created_at, updated_at FROM channels WHERE id = ?`, id,
	).Scan(&c.ID, &c.Name, &c.Type, &c.BaseURL, &c.APIKey, &c.Status, &c.Priority, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (r *ChannelRepo) Create(c *Channel) (int64, error) {
	result, err := r.db.Exec(
		`INSERT INTO channels (name, type, base_url, api_key, status, priority) VALUES (?, ?, ?, ?, ?, ?)`,
		c.Name, c.Type, c.BaseURL, c.APIKey, c.Status, c.Priority,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *ChannelRepo) Update(c *Channel) error {
	_, err := r.db.Exec(
		`UPDATE channels SET name=?, type=?, base_url=?, api_key=?, status=?, priority=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		c.Name, c.Type, c.BaseURL, c.APIKey, c.Status, c.Priority, c.ID,
	)
	return err
}

func (r *ChannelRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM channels WHERE id = ?`, id)
	return err
}
