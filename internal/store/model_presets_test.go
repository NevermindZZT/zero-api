package store

import (
	"path/filepath"
	"testing"

	"github.com/never/zero-api/internal/config"
)

func TestApplyPresetsOnlyUpdatesUnmodifiedModels(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "presets.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO channels (name, type, base_url) VALUES ('test', 'openai', 'http://example.test')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE models ADD COLUMN user_modified INTEGER DEFAULT 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE models ADD COLUMN pricing_rules TEXT DEFAULT '[]'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO models (channel_id, model_id, pricing_input, pricing_output, user_modified) VALUES (1, 'gpt-5.4', 0, 0, 0), (1, 'manual-model', 9, 9, 1)`); err != nil {
		t.Fatal(err)
	}

	repo := NewModelRepo(db)
	updated, err := repo.ApplyPresets(map[string]config.ModelDefault{
		"gpt-5.4":      {PricingInput: 2.5, PricingOutput: 15, ContextWindow: 1048576},
		"manual-model": {PricingInput: 1, PricingOutput: 2, ContextWindow: 1000},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("updated=%d, want 1", updated)
	}
	var input, output float64
	if err := db.QueryRow(`SELECT pricing_input, pricing_output FROM models WHERE model_id='gpt-5.4'`).Scan(&input, &output); err != nil {
		t.Fatal(err)
	}
	if input != 2.5 || output != 15 {
		t.Fatalf("default pricing not applied: %v/%v", input, output)
	}
	if err := db.QueryRow(`SELECT pricing_input, pricing_output FROM models WHERE model_id='manual-model'`).Scan(&input, &output); err != nil {
		t.Fatal(err)
	}
	if input != 9 || output != 9 {
		t.Fatalf("manual pricing was overwritten: %v/%v", input, output)
	}
}
