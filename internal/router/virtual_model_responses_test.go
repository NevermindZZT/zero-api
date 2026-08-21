package router

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/never/zero-api/internal/store"
)

func TestVirtualModelResponsesPreservesNativeInput(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "responses-alias.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO channels (name, type, base_url, status) VALUES ('test', 'responses', 'http://example.test', 'active')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO models (channel_id, model_id, status) VALUES (1, 'gpt-5.6-luna', 'active')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO virtual_models (name, main_model, status) VALUES ('agent-model', 'gpt-5.6-luna', 'active')`); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{
		"model":"agent-model",
		"previous_response_id":"resp_prev",
		"input":[
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"reasoning summary"}]},
			{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"tool result"}]}
		]
	}`)
	r := NewVirtualModelRouter(store.NewVirtualModelRepo(db), store.NewModelRepo(db), store.NewChannelRepo(db), nil)
	result := r.Transform(&Context{Model: "agent-model", Protocol: ProtocolResponses, RawBody: body})
	if result == nil || result.Handled {
		t.Fatalf("expected routed request, got %+v", result)
	}
	if !strings.Contains(string(result.NewBody), `"summary"`) || !strings.Contains(string(result.NewBody), `"call_id":"call_1"`) || !strings.Contains(string(result.NewBody), `"previous_response_id"`) {
		t.Fatalf("native Responses fields were lost: %s", result.NewBody)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(result.NewBody, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["model"] != "gpt-5.6-luna" {
		t.Fatalf("model not replaced: %v", parsed["model"])
	}
}
