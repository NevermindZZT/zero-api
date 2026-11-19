package upstream

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/never/zero-api/internal/config"
	"github.com/never/zero-api/internal/store"
)

func TestFetchCompleteModelsRejectsUnsafeResponses(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		headers    map[string]string
	}{
		{"empty", `{"data":[]}`, 200, nil}, {"invalid-json", `{"data":[`, 200, nil}, {"missing-data", `{}`, 200, nil}, {"null-id", `{"data":[{}]}`, 200, nil}, {"blank-id", `{"data":[{"id":""}]}`, 200, nil}, {"duplicate", `{"data":[{"id":"x"},{"id":"x"}]}`, 200, nil}, {"error status", `{"data":[{"id":"x"}]}`, 500, nil}, {"partial status", `{"data":[{"id":"x"}]}`, 206, nil}, {"has more", `{"data":[{"id":"x"}],"has_more":true}`, 200, nil}, {"next token", `{"data":[{"id":"x"}],"next_page_token":"other"}`, 200, nil}, {"truncated", `{"data":[{"id":"x"}],"truncated":true}`, 200, nil}, {"total mismatch", `{"data":[{"id":"x"}],"total":2}`, 200, nil}, {"link next", `{"data":[{"id":"x"}]}`, 200, map[string]string{"Link": `</v1/models?page=2>; rel="next"`}}, {"too large", `{"data":[{"id":"x"}],"padding":"` + strings.Repeat("x", maxPreviewBody) + `"}`, 200, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			_, err := fetchCompleteModels(context.Background(), &store.Channel{Type: "openai", BaseURL: srv.URL})
			if err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestSafeSyncBaseURLWithV1(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		fmt.Fprint(w, `{"data":[{"id":"one"}]}`)
	}))
	defer srv.Close()
	_, err := fetchCompleteModels(context.Background(), &store.Channel{Type: "openai", BaseURL: srv.URL + "/v1"})
	if err != nil || got != "/v1/models" {
		t.Fatalf("path=%s err=%v", got, err)
	}
}

func TestFetchAnthropicAndGeminiPagination(t *testing.T) {
	t.Run("anthropic", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("after_id") == "a" {
				fmt.Fprint(w, `{"data":[{"id":"b","display_name":"Bee"}],"has_more":false,"last_id":"b"}`)
			} else {
				fmt.Fprint(w, `{"data":[{"id":"a","display_name":"Aye"}],"has_more":true,"last_id":"a"}`)
			}
		}))
		defer srv.Close()
		m, err := fetchCompleteModels(context.Background(), &store.Channel{Type: "anthropic", BaseURL: srv.URL})
		if err != nil || len(m) != 2 || m[0].ID != "a" || m[1].ID != "b" {
			t.Fatalf("actual response not parsed: %#v %v", m, err)
		}
	})
	t.Run("anthropic null pagination and mismatched cursor", func(t *testing.T) {
		for _, body := range []string{`{"data":[{"id":"a"}],"has_more":null}`, `{"data":[{"id":"a"}],"has_more":true,"last_id":"different"}`} {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			_, err := fetchCompleteModels(context.Background(), &store.Channel{Type: "anthropic", BaseURL: srv.URL})
			srv.Close()
			if err == nil {
				t.Fatalf("accepted incomplete page %s", body)
			}
		}
	})
	t.Run("anthropic missing paging", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":[{"id":"a"}]}`) }))
		defer srv.Close()
		if _, err := fetchCompleteModels(context.Background(), &store.Channel{Type: "anthropic", BaseURL: srv.URL}); err == nil {
			t.Fatal("missing pagination accepted")
		}
	})
	t.Run("gemini", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("pageToken") == "next" {
				fmt.Fprint(w, `{"models":[{"name":"models/b"}]}`)
			} else {
				fmt.Fprint(w, `{"models":[{"name":"models/a"}],"nextPageToken":"next"}`)
			}
		}))
		defer srv.Close()
		m, err := fetchCompleteModels(context.Background(), &store.Channel{Type: "gemini", BaseURL: srv.URL})
		if err != nil || len(m) != 2 || m[1].ID != "b" {
			t.Fatalf("models=%+v error=%v", m, err)
		}
	})
	t.Run("pagination loop", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"models":[{"name":"models/a"}],"nextPageToken":"next"}`)
		}))
		defer srv.Close()
		if _, err := fetchCompleteModels(context.Background(), &store.Channel{Type: "gemini", BaseURL: srv.URL}); err == nil {
			t.Fatal("pagination loop accepted")
		}
	})
}

type syncFixture struct {
	db        *store.DB
	syncer    *Syncer
	channels  *store.ChannelRepo
	models    *store.ModelRepo
	channelID int64
	url       string
	mu        sync.Mutex
	body      string
}

func newSyncFixture(t *testing.T) *syncFixture {
	t.Helper()
	f := &syncFixture{body: `{"data":[{"id":"keep"},{"id":"new"}]}`}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.mu.Lock(); defer f.mu.Unlock(); fmt.Fprint(w, f.body) }))
	t.Cleanup(srv.Close)
	f.url = srv.URL
	db, err := store.Open(filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f.db = db
	f.channels = store.NewChannelRepo(db)
	f.models = store.NewModelRepo(db)
	f.channelID, err = f.channels.Create(&store.Channel{Name: "test", Type: "openai", BaseURL: srv.URL, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	f.syncer = NewSyncer(f.channels, f.models, config.NewModelPresets(filepath.Join(t.TempDir(), "none"), nil))
	return f
}
func (f *syncFixture) setBody(body string) { f.mu.Lock(); defer f.mu.Unlock(); f.body = body }
func (f *syncFixture) insert(t *testing.T, channel int64, id string, modified bool) int64 {
	t.Helper()
	result, err := f.db.Exec(`INSERT INTO models(channel_id,model_id,display_name,status,user_modified,pricing_rules) VALUES(?,?,?,'active',?,'')`, channel, id, id, modified)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := result.LastInsertId()
	return n
}
func (f *syncFixture) preview(t *testing.T) *PreviewResult {
	t.Helper()
	p, err := f.syncer.PreviewModels(context.Background(), f.channelID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSafeSyncSelectionAndHistory(t *testing.T) {
	f := newSyncFixture(t)
	f.insert(t, f.channelID, "keep", true)
	stale := f.insert(t, f.channelID, "stale", true)
	second, err := f.channels.Create(&store.Channel{Name: "other", Type: "openai", BaseURL: f.url, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	f.insert(t, second, "stale", false)
	_, err = f.db.Exec(`INSERT INTO usage_records(model_id,request_model,total_tokens) VALUES(?, 'stale',42)`, stale)
	if err != nil {
		t.Fatal(err)
	}
	p := f.preview(t)
	if len(p.Models) != 2 || len(p.StaleModels) != 1 || !p.StaleModels[0].UserModified {
		t.Fatalf("preview=%+v", p)
	}
	count, deleted, err := f.syncer.ConfirmModels(context.Background(), f.channelID, p.PreviewID, []string{"new"})
	if err != nil || count != 1 || deleted != 1 {
		t.Fatalf("count=%d deleted=%d err=%v", count, deleted, err)
	}
	var got string
	if err := f.db.QueryRow(`SELECT group_concat(model_id,',') FROM (SELECT model_id FROM models WHERE channel_id=? ORDER BY model_id)`, f.channelID).Scan(&got); err != nil || got != "keep,new" {
		t.Fatalf("models=%s err=%v", got, err)
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM models WHERE channel_id=? AND model_id='stale'`, second).Scan(&n); err != nil || n != 1 {
		t.Fatal("other channel affected")
	}
	var linked interface{}
	var tokens int
	if err := f.db.QueryRow(`SELECT model_id,total_tokens FROM usage_records WHERE request_model='stale'`).Scan(&linked, &tokens); err != nil || linked != nil || tokens != 42 {
		t.Fatalf("usage reference=%v tokens=%d err=%v", linked, tokens, err)
	}
	if _, _, err := f.syncer.ConfirmModels(context.Background(), f.channelID, p.PreviewID, nil); !errors.Is(err, ErrPreviewConflict) {
		t.Fatalf("replay=%v", err)
	}
}

func TestSafeSyncConflictsAndAtomicRollback(t *testing.T) {
	t.Run("selected user-modified model preserved", func(t *testing.T) {
		f := newSyncFixture(t)
		id := f.insert(t, f.channelID, "keep", true)
		if _, err := f.db.Exec(`UPDATE models SET context_window=777, display_name='Custom' WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		p := f.preview(t)
		if _, _, err := f.syncer.ConfirmModels(context.Background(), f.channelID, p.PreviewID, []string{"keep"}); err != nil {
			t.Fatal(err)
		}
		var window int
		var name string
		if err := f.db.QueryRow(`SELECT context_window,display_name FROM models WHERE id=?`, id).Scan(&window, &name); err != nil || window != 777 || name != "Custom" {
			t.Fatalf("settings lost: %d %s %v", window, name, err)
		}
	})
	t.Run("local edited", func(t *testing.T) {
		f := newSyncFixture(t)
		id := f.insert(t, f.channelID, "stale", false)
		p := f.preview(t)
		_, err := f.db.Exec(`UPDATE models SET user_modified=1 WHERE id=?`, id)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = f.syncer.ConfirmModels(context.Background(), f.channelID, p.PreviewID, nil)
		if !errors.Is(err, ErrPreviewConflict) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("channel changed", func(t *testing.T) {
		f := newSyncFixture(t)
		f.insert(t, f.channelID, "stale", false)
		p := f.preview(t)
		_, err := f.db.Exec(`UPDATE channels SET name='changed' WHERE id=?`, f.channelID)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = f.syncer.ConfirmModels(context.Background(), f.channelID, p.PreviewID, nil)
		if !errors.Is(err, ErrPreviewConflict) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("upstream metadata changed", func(t *testing.T) {
		f := newSyncFixture(t)
		f.setBody(`{"data":[{"id":"keep","context_length":100},{"id":"new"}]}`)
		p := f.preview(t)
		f.setBody(`{"data":[{"id":"keep","context_length":200},{"id":"new"}]}`)
		_, _, err := f.syncer.ConfirmModels(context.Background(), f.channelID, p.PreviewID, nil)
		if !errors.Is(err, ErrPreviewConflict) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("upstream changed", func(t *testing.T) {
		f := newSyncFixture(t)
		p := f.preview(t)
		f.setBody(`{"data":[{"id":"different"}]}`)
		_, _, err := f.syncer.ConfirmModels(context.Background(), f.channelID, p.PreviewID, nil)
		if !errors.Is(err, ErrPreviewConflict) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		f := newSyncFixture(t)
		p := f.preview(t)
		f.syncer.previewMu.Lock()
		f.syncer.previews[p.PreviewID].expires = time.Now().Add(-time.Second)
		f.syncer.previewMu.Unlock()
		_, _, err := f.syncer.ConfirmModels(context.Background(), f.channelID, p.PreviewID, nil)
		if !errors.Is(err, ErrPreviewConflict) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("invalid choice", func(t *testing.T) {
		f := newSyncFixture(t)
		p := f.preview(t)
		_, _, err := f.syncer.ConfirmModels(context.Background(), f.channelID, p.PreviewID, []string{"not-upstream"})
		if err == nil {
			t.Fatal("invalid choice accepted")
		}
	})
	t.Run("rollback", func(t *testing.T) {
		f := newSyncFixture(t)
		f.insert(t, f.channelID, "stale", false)
		p := f.preview(t)
		_, err := f.db.Exec(`CREATE TRIGGER fail_model_delete BEFORE DELETE ON models BEGIN SELECT RAISE(ABORT,'injected'); END`)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = f.syncer.ConfirmModels(context.Background(), f.channelID, p.PreviewID, []string{"new"})
		if err == nil {
			t.Fatal("expected failure")
		}
		var n int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM models WHERE channel_id=? AND model_id='new'`, f.channelID).Scan(&n); err != nil || n != 0 {
			t.Fatal("upsert was not rolled back")
		}
	})
	t.Run("single use concurrently", func(t *testing.T) {
		f := newSyncFixture(t)
		p := f.preview(t)
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() {
				_, _, err := f.syncer.ConfirmModels(context.Background(), f.channelID, p.PreviewID, nil)
				results <- err
			}()
		}
		a, b := <-results, <-results
		if !((a == nil && errors.Is(b, ErrPreviewConflict)) || (b == nil && errors.Is(a, ErrPreviewConflict))) {
			t.Fatalf("err1=%v err2=%v", a, b)
		}
	})
}
