package store

import (
	"time"
)

// ChannelBalance 渠道余额/订阅状态（一个渠道一条）
type ChannelBalance struct {
	ID             int64     `json:"id"`
	ChannelID      int64     `json:"channel_id"`
	Balance        float64   `json:"balance"`
	Currency       string    `json:"currency"`
	UsedAmount     float64   `json:"used_amount"`
	PlanType       string    `json:"plan_type"`
	PlanStatus     string    `json:"plan_status"`
	RenewsAt       string    `json:"renews_at"`
	ExpiresAt      string    `json:"expires_at"`
	TokenQuota     int64     `json:"token_quota"`
	TokenUsed      int64     `json:"token_used"`
	TokenRemaining int64     `json:"token_remaining"`
	Provider       string    `json:"provider"`
	Status         string    `json:"status"` // ok / warning / error / unsupported
	ErrorMsg       string    `json:"error_msg"`
	RawData        string    `json:"raw_data"`
	LastCheckedAt  time.Time `json:"last_checked_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ChannelBalanceRepo 余额数据访问
type ChannelBalanceRepo struct {
	db *DB
}

func NewChannelBalanceRepo(db *DB) *ChannelBalanceRepo {
	return &ChannelBalanceRepo{db: db}
}

// Upsert 插入或更新渠道余额记录
func (r *ChannelBalanceRepo) Upsert(b *ChannelBalance) error {
	_, err := r.db.Exec(
		`INSERT INTO channel_balances
			(channel_id, balance, currency, used_amount, plan_type, plan_status, renews_at, expires_at,
			 token_quota, token_used, token_remaining, provider, status, error_msg, raw_data, last_checked_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
			balance = excluded.balance,
			currency = excluded.currency,
			used_amount = excluded.used_amount,
			plan_type = excluded.plan_type,
			plan_status = excluded.plan_status,
			renews_at = excluded.renews_at,
			expires_at = excluded.expires_at,
			token_quota = excluded.token_quota,
			token_used = excluded.token_used,
			token_remaining = excluded.token_remaining,
			provider = excluded.provider,
			status = excluded.status,
			error_msg = excluded.error_msg,
			raw_data = excluded.raw_data,
			last_checked_at = excluded.last_checked_at,
			updated_at = excluded.updated_at`,
		b.ChannelID, b.Balance, b.Currency, b.UsedAmount, b.PlanType, b.PlanStatus, b.RenewsAt, b.ExpiresAt,
		b.TokenQuota, b.TokenUsed, b.TokenRemaining, b.Provider, b.Status, b.ErrorMsg, b.RawData,
		b.LastCheckedAt, b.UpdatedAt,
	)
	return err
}

// GetByChannel 获取渠道余额（不存在返回 nil, nil）
func (r *ChannelBalanceRepo) GetByChannel(channelID int64) (*ChannelBalance, error) {
	row := r.db.QueryRow(
		`SELECT id, channel_id, balance, currency, used_amount, plan_type, plan_status, renews_at, expires_at,
		        token_quota, token_used, token_remaining, provider, status, error_msg, raw_data, last_checked_at, updated_at
		 FROM channel_balances WHERE channel_id = ?`, channelID,
	)
	var b ChannelBalance
	err := row.Scan(&b.ID, &b.ChannelID, &b.Balance, &b.Currency, &b.UsedAmount, &b.PlanType, &b.PlanStatus,
		&b.RenewsAt, &b.ExpiresAt, &b.TokenQuota, &b.TokenUsed, &b.TokenRemaining, &b.Provider, &b.Status,
		&b.ErrorMsg, &b.RawData, &b.LastCheckedAt, &b.UpdatedAt)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

// ListAll 获取所有渠道余额（LEFT JOIN channels 带名称）
func (r *ChannelBalanceRepo) ListAll() ([]ChannelBalance, error) {
	rows, err := r.db.Query(
		`SELECT cb.id, cb.channel_id, cb.balance, cb.currency, cb.used_amount, cb.plan_type, cb.plan_status,
		        cb.renews_at, cb.expires_at, cb.token_quota, cb.token_used, cb.token_remaining, cb.provider,
		        cb.status, cb.error_msg, cb.raw_data, cb.last_checked_at, cb.updated_at
		 FROM channel_balances cb
		 ORDER BY cb.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ChannelBalance
	for rows.Next() {
		var b ChannelBalance
		if err := rows.Scan(&b.ID, &b.ChannelID, &b.Balance, &b.Currency, &b.UsedAmount, &b.PlanType, &b.PlanStatus,
			&b.RenewsAt, &b.ExpiresAt, &b.TokenQuota, &b.TokenUsed, &b.TokenRemaining, &b.Provider, &b.Status,
			&b.ErrorMsg, &b.RawData, &b.LastCheckedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, b)
	}
	return list, rows.Err()
}

// DeleteByChannel 删除渠道余额记录（渠道删除时调用）
func (r *ChannelBalanceRepo) DeleteByChannel(channelID int64) error {
	_, err := r.db.Exec(`DELETE FROM channel_balances WHERE channel_id = ?`, channelID)
	return err
}
