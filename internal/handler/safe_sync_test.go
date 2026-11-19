package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/never/zero-api/internal/config"
	"github.com/never/zero-api/internal/store"
	"github.com/never/zero-api/internal/upstream"
)

func TestSafeSyncEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":[{"id":"upstream"}]}`) }))
	defer provider.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cr, mr := store.NewChannelRepo(db), store.NewModelRepo(db)
	id, err := cr.Create(&store.Channel{Name: "channel", Type: "openai", BaseURL: provider.URL, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO models(channel_id,model_id,display_name,status,user_modified,pricing_rules) VALUES(?, 'obsolete','obsolete','active',1,'')`, id)
	if err != nil {
		t.Fatal(err)
	}
	h := NewSyncHandler(upstream.NewSyncer(cr, mr, config.NewModelPresets(filepath.Join(t.TempDir(), "none"), nil)), mr, "")
	calls := 0
	h.SetOnSynced(func() { calls++ })
	router := gin.New()
	router.POST("/channels/:id/sync", h.SyncModels)
	router.POST("/channels/:id/sync/preview", h.PreviewModels)
	router.POST("/channels/:id/sync/confirm", h.ConfirmModels)
	endpoint := fmt.Sprintf("/channels/%d/sync", id)
	request := func(path string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, endpoint+path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	preview := request("/preview", nil)
	if preview.Code != 200 {
		t.Fatalf("preview=%d %s", preview.Code, preview.Body)
	}
	var p struct {
		PreviewID string `json:"preview_id"`
		Models    []struct {
			ID       string `json:"id"`
			Existing bool   `json:"existing"`
		} `json:"models"`
		Stale []struct {
			ModelID      string `json:"model_id"`
			UserModified bool   `json:"user_modified"`
		} `json:"stale_models"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &p); err != nil || p.PreviewID == "" || len(p.Models) != 1 || len(p.Stale) != 1 || !p.Stale[0].UserModified {
		t.Fatalf("preview=%+v err=%v", p, err)
	}
	raw, _ := json.Marshal(map[string]interface{}{"preview_id": p.PreviewID, "selected_model_ids": []string{}})
	confirm := request("/confirm", raw)
	if confirm.Code != 200 || calls != 1 {
		t.Fatalf("confirm=%d %s callback=%d", confirm.Code, confirm.Body, calls)
	}
	var result struct {
		Count   int `json:"count"`
		Deleted int `json:"deleted_count"`
	}
	if err := json.Unmarshal(confirm.Body.Bytes(), &result); err != nil || result.Count != 0 || result.Deleted != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	replay := request("/confirm", raw)
	if replay.Code != http.StatusConflict || calls != 1 {
		t.Fatalf("replay=%d callback=%d", replay.Code, calls)
	}
	invalid := request("/confirm", []byte(`{"preview_id":"abc","selected_model_ids":null}`))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("null selection=%d", invalid.Code)
	}
	if _, err := db.Exec(`INSERT INTO models(channel_id,model_id,display_name,status,pricing_rules) VALUES(?, 'legacy-local','legacy-local','active','')`, id); err != nil {
		t.Fatal(err)
	}
	legacy := request("", nil)
	if legacy.Code != http.StatusOK || calls != 2 {
		t.Fatalf("legacy=%d callback=%d", legacy.Code, calls)
	}
	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM models WHERE channel_id=? AND model_id='legacy-local'`, id).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("legacy removed model: %d %v", remaining, err)
	}
}
