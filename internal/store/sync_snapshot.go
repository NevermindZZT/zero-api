package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// syncFingerprint hashes all persisted channel/model fields, not timestamps alone.
// It intentionally includes credentials, but exposes only an irreversible digest.
func syncFingerprint(tx *sql.Tx, channelID int64) (string, error) {
	all := make([][][]interface{}, 0, 2)
	for _, query := range []string{"SELECT * FROM channels WHERE id=?", "SELECT * FROM models WHERE channel_id=? ORDER BY id"} {
		rows, err := tx.Query(query, channelID)
		if err != nil {
			return "", err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		group := make([][]interface{}, 0)
		for rows.Next() {
			vals := make([]interface{}, len(columns))
			ptrs := make([]interface{}, len(columns))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return "", err
			}
			// Copy database-owned byte slices before advancing the cursor.
			for i, v := range vals {
				if b, ok := v.([]byte); ok {
					vals[i] = string(b)
				}
			}
			group = append(group, vals)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
		all = append(all, group)
	}
	if len(all[0]) != 1 {
		return "", ErrSyncStateChanged
	}
	encoded, err := json.Marshal(all)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// SyncFingerprint is a fresh, consistent snapshot, bypassing model/channel caches.
func (r *ModelRepo) SyncFingerprint(channelID int64) (string, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	fingerprint, err := syncFingerprint(tx, channelID)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return fingerprint, nil
}

var ErrSyncStateChanged = fmt.Errorf("渠道或模型数据已变更，请重新预览")

// ApplySync checks the complete channel and model snapshot inside the write
// transaction; any failure rolls back both upserts and deletions.
func (r *ModelRepo) ApplySync(channelID int64, expected string, selected []*Model, stale []int64) (int, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	actual, err := syncFingerprint(tx, channelID)
	if err != nil {
		return 0, err
	}
	if actual != expected {
		return 0, ErrSyncStateChanged
	}
	for _, model := range selected {
		if model.ChannelID != channelID {
			return 0, fmt.Errorf("渠道不匹配")
		}
		if _, err := upsertModel(tx, model); err != nil {
			return 0, err
		}
	}
	deleted := 0
	for _, id := range stale {
		if _, err := tx.Exec("UPDATE usage_records SET model_id=NULL WHERE model_id=?", id); err != nil {
			return 0, err
		}
		result, err := tx.Exec("DELETE FROM models WHERE id=? AND channel_id=?", id, channelID)
		if err != nil {
			return 0, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if n != 1 {
			return 0, ErrSyncStateChanged
		}
		deleted += int(n)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	r.InvalidateModelCache()
	return deleted, nil
}
