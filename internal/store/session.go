package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

const SessionTTL = 30 * 24 * time.Hour
const SessionRenewBefore = 15 * 24 * time.Hour

// AuthSession contains no raw identifier; the cookie is only returned by Create.
type AuthSession struct {
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
	RenewedAt time.Time
}

type SessionRepo struct {
	db      *DB
	writeMu sync.Mutex
}

func NewSessionRepo(db *DB) *SessionRepo { return &SessionRepo{db: db} }

func sessionTokenHash(token string) (string, bool) {
	if len(token) != 43 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 32 {
		return "", false
	}
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:]), true
}

// Create rotates this browser's old session in the same transaction as inserting
// a new random session. The database never stores a reusable cookie value.
func (r *SessionRepo) Create(ctx context.Context, username, accountVersion, previousToken string, now time.Time) (string, *AuthSession, error) {
	if username == "" || accountVersion == "" {
		return "", nil, errors.New("invalid session account")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	digest, _ := sessionTokenHash(token)
	now = now.UTC().Truncate(time.Second)
	session := &AuthSession{Username: username, CreatedAt: now, ExpiresAt: now.Add(SessionTTL), RenewedAt: now}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM admin_sessions WHERE expires_at <= ?`, now.Unix()); err != nil {
		return "", nil, err
	}
	if previous, ok := sessionTokenHash(previousToken); ok {
		if _, err = tx.ExecContext(ctx, `DELETE FROM admin_sessions WHERE id_hash = ?`, previous); err != nil {
			return "", nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO admin_sessions(id_hash,username,account_version,created_at,expires_at,renewed_at) VALUES(?,?,?,?,?,?)`, digest, username, accountVersion, session.CreatedAt.Unix(), session.ExpiresAt.Unix(), session.RenewedAt.Unix()); err != nil {
		return "", nil, err
	}
	if err = tx.Commit(); err != nil {
		return "", nil, err
	}
	return token, session, nil
}

func (r *SessionRepo) find(ctx context.Context, digest, username, accountVersion string, now time.Time) (*AuthSession, error) {
	var result AuthSession
	var created, expires, renewed int64
	err := r.db.QueryRowContext(ctx, `SELECT username,created_at,expires_at,renewed_at FROM admin_sessions WHERE id_hash=? AND username=? AND account_version=? AND expires_at>?`, digest, username, accountVersion, now.Unix()).Scan(&result.Username, &created, &expires, &renewed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result.CreatedAt = time.Unix(created, 0).UTC()
	result.ExpiresAt = time.Unix(expires, 0).UTC()
	result.RenewedAt = time.Unix(renewed, 0).UTC()
	return &result, nil
}

// Authenticate is read-only until strictly less than 15 days remain. Renewal is
// a conditional UPDATE, never INSERT: concurrent logout/expiration cannot revive
// a session, and only one concurrent activity request performs the renewal write.
func (r *SessionRepo) Authenticate(ctx context.Context, token, username, accountVersion string, now time.Time) (*AuthSession, bool, error) {
	digest, ok := sessionTokenHash(token)
	if !ok || username == "" || accountVersion == "" {
		return nil, false, nil
	}
	now = now.UTC().Truncate(time.Second)
	session, err := r.find(ctx, digest, username, accountVersion, now)
	if err != nil || session == nil {
		return session, false, err
	}
	if session.ExpiresAt.Sub(now) >= SessionRenewBefore {
		return session, false, nil
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	session, err = r.find(ctx, digest, username, accountVersion, now)
	if err != nil || session == nil {
		return session, false, err
	}
	if session.ExpiresAt.Sub(now) >= SessionRenewBefore {
		return session, false, nil
	}
	expires := now.Add(SessionTTL)
	result, err := r.db.ExecContext(ctx, `UPDATE admin_sessions SET expires_at=?,renewed_at=? WHERE id_hash=? AND username=? AND account_version=? AND expires_at>? AND expires_at<?`, expires.Unix(), now.Unix(), digest, username, accountVersion, now.Unix(), now.Add(SessionRenewBefore).Unix())
	if err != nil {
		return nil, false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if count == 1 {
		session.ExpiresAt = expires
		session.RenewedAt = now
		return session, true, nil
	}
	// Another request may have renewed or revoked it while we read. Reload rather
	// than returning the stale expiry or recreating a deleted record.
	session, err = r.find(ctx, digest, username, accountVersion, now)
	return session, false, err
}

func (r *SessionRepo) Revoke(ctx context.Context, token string) error {
	digest, ok := sessionTokenHash(token)
	if !ok {
		return nil
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	_, err := r.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE id_hash=?`, digest)
	return err
}
