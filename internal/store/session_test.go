package store

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sessionTestRepo(t *testing.T) (*SessionRepo, *DB, time.Time) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewSessionRepo(db), db, time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC)
}
func createTestSession(t *testing.T, repo *SessionRepo, now time.Time) (string, *AuthSession) {
	t.Helper()
	token, session, err := repo.Create(context.Background(), "test-admin", "test-version", "", now)
	if err != nil {
		t.Fatal(err)
	}
	return token, session
}

func TestSessionCreatedFor30DaysAndOnlyHashStored(t *testing.T) {
	repo, db, now := sessionTestRepo(t)
	token, session := createTestSession(t, repo, now)
	if len(token) != 43 || session.ExpiresAt.Sub(now) != 30*24*time.Hour {
		t.Fatal("wrong token format or TTL")
	}
	var hash, username, version string
	if err := db.QueryRow(`SELECT id_hash,username,account_version FROM admin_sessions`).Scan(&hash, &username, &version); err != nil {
		t.Fatal(err)
	}
	expected, _ := sessionTokenHash(token)
	if hash != expected || hash == token || len(hash) != 64 || username != "test-admin" || version != "test-version" {
		t.Fatal("invalid persisted metadata")
	}
	valid, renewed, err := repo.Authenticate(context.Background(), token, "test-admin", "test-version", now)
	if err != nil || valid == nil || renewed {
		t.Fatal("new session authentication failed")
	}
}
func TestSessionRenewsOnlyWhenStrictlyLessThan15DaysRemain(t *testing.T) {
	repo, _, now := sessionTestRepo(t)
	token, original := createTestSession(t, repo, now)
	for _, age := range []time.Duration{14 * 24 * time.Hour, 15 * 24 * time.Hour} {
		session, renewed, err := repo.Authenticate(context.Background(), token, "test-admin", "test-version", now.Add(age))
		if err != nil || session == nil || renewed || !session.ExpiresAt.Equal(original.ExpiresAt) {
			t.Fatalf("renewed at age %v", age)
		}
	}
	activity := now.Add(15*24*time.Hour + time.Second)
	session, renewed, err := repo.Authenticate(context.Background(), token, "test-admin", "test-version", activity)
	if err != nil || session == nil || !renewed || !session.ExpiresAt.Equal(activity.Add(SessionTTL)) {
		t.Fatal("activity did not renew to 30 days")
	}
	session, renewed, err = repo.Authenticate(context.Background(), token, "test-admin", "test-version", activity.Add(time.Hour))
	if err != nil || session == nil || renewed || !session.ExpiresAt.Equal(activity.Add(SessionTTL)) {
		t.Fatal("unnecessary per-request extension")
	}
}
func TestRegularActivityKeepsSessionAliveWithoutAbsoluteCap(t *testing.T) {
	repo, _, now := sessionTestRepo(t)
	token, _ := createTestSession(t, repo, now)
	for i := 1; i <= 20; i++ {
		activity := now.Add(time.Duration(i) * 16 * 24 * time.Hour)
		session, renewed, err := repo.Authenticate(context.Background(), token, "test-admin", "test-version", activity)
		if err != nil || session == nil || !renewed || !session.ExpiresAt.Equal(activity.Add(SessionTTL)) {
			t.Fatalf("session failed at renewal %d", i)
		}
	}
}
func TestExpiredSessionCannotBeRenewedOrResurrected(t *testing.T) {
	repo, _, now := sessionTestRepo(t)
	token, session := createTestSession(t, repo, now)
	for _, at := range []time.Time{session.ExpiresAt, session.ExpiresAt.Add(time.Second), session.ExpiresAt.Add(100 * 24 * time.Hour)} {
		found, renewed, err := repo.Authenticate(context.Background(), token, "test-admin", "test-version", at)
		if err != nil || found != nil || renewed {
			t.Fatal("expired session revived")
		}
	}
}
func TestSessionVersionUserTamperingAndRevocation(t *testing.T) {
	repo, db, now := sessionTestRepo(t)
	token, _ := createTestSession(t, repo, now)
	for _, value := range []struct{ token, user, version string }{
		{token, "other-user", "test-version"}, {token, "test-admin", "changed-version"},
		{"not-a-cookie", "test-admin", "test-version"}, {strings.Repeat("a", 43), "test-admin", "test-version"},
	} {
		found, renewed, err := repo.Authenticate(context.Background(), value.token, value.user, value.version, now.Add(20*24*time.Hour))
		if err != nil || found != nil || renewed {
			t.Fatal("invalid account or identifier accepted")
		}
	}
	if err := repo.Revoke(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if err := repo.Revoke(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	found, renewed, err := repo.Authenticate(context.Background(), token, "test-admin", "test-version", now.Add(20*24*time.Hour))
	if err != nil || found != nil || renewed {
		t.Fatal("revoked session revived")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM admin_sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked row remains")
	}
}
func TestLoginRotatesPreviousBrowserSession(t *testing.T) {
	repo, _, now := sessionTestRepo(t)
	old, _ := createTestSession(t, repo, now)
	token, _, err := repo.Create(context.Background(), "test-admin", "test-version", old, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if token == old {
		t.Fatal("session fixation: reused old identifier")
	}
	found, _, err := repo.Authenticate(context.Background(), old, "test-admin", "test-version", now.Add(time.Hour))
	if err != nil || found != nil {
		t.Fatal("old session not revoked")
	}
	found, _, err = repo.Authenticate(context.Background(), token, "test-admin", "test-version", now.Add(time.Hour))
	if err != nil || found == nil {
		t.Fatal("replacement session invalid")
	}
}
func TestConcurrentActivityRenewsOnePersistedRow(t *testing.T) {
	repo, db, now := sessionTestRepo(t)
	token, _ := createTestSession(t, repo, now)
	activity := now.Add(20 * 24 * time.Hour)
	var group sync.WaitGroup
	var renewals atomic.Int32
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			session, renewed, err := repo.Authenticate(context.Background(), token, "test-admin", "test-version", activity)
			if err != nil || session == nil || !session.ExpiresAt.Equal(activity.Add(SessionTTL)) {
				t.Errorf("concurrent auth failed: %v", err)
				return
			}
			if renewed {
				renewals.Add(1)
			}
		}()
	}
	group.Wait()
	if renewals.Load() != 1 {
		t.Fatalf("renewal writes=%d, want1", renewals.Load())
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM admin_sessions`).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent renewal duplicated row")
	}
}
func TestSessionSurvivesDatabaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	token, _ := createTestSession(t, NewSessionRepo(db), now)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	session, renewed, err := NewSessionRepo(db).Authenticate(context.Background(), token, "test-admin", "test-version", now.Add(20*24*time.Hour))
	if err != nil || session == nil || !renewed {
		t.Fatal("restart lost valid session")
	}
}
func TestSessionDatabaseErrorIsNotAnInvalidLogin(t *testing.T) {
	repo, db, now := sessionTestRepo(t)
	token, _ := createTestSession(t, repo, now)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Authenticate(context.Background(), token, "test-admin", "test-version", now); err == nil {
		t.Fatal("database failure swallowed as anonymous session")
	}
}
