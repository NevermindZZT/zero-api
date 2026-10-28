package adapter

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestResponsesNonStreamingFailureIsNotCompletion(t *testing.T) {
	for _, body := range []string{
		`{"id":"resp_f","status":"failed","output":[]}`,
		`{"id":"resp_f","status":"error","output":[]}`,
		`{"id":"resp_f","status":"cancelled","output":[]}`,
		`{"error":{"type":"server_error","message":"provider-secret-detail"}}`,
		`{"status":"completed","error":{"message":"provider-secret-detail"},"output":[]}`,
		`{"error":"provider-secret-detail"}`,
	} {
		t.Run(body, func(t *testing.T) {
			out, err := (&ResponsesAdapter{}).ConvertResponse([]byte(body))
			if err == nil || len(out) != 0 {
				t.Fatalf("failed response became completion: output=%s err=%v", out, err)
			}
			if strings.Contains(err.Error(), "provider-secret-detail") {
				t.Fatal("provider detail leaked through conversion error")
			}
		})
	}
}

func TestResponsesNonStreamingIncompleteAndNullError(t *testing.T) {
	for _, tc := range []struct{ status, reason string }{{"completed", "stop"}, {"incomplete", "length"}} {
		body := []byte(`{"id":"resp_partial","status":"` + tc.status + `","error":null,"model":"gpt-5","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`)
		out, err := (&ResponsesAdapter{}).ConvertResponse(body)
		if err != nil {
			t.Fatal(err)
		}
		var response OpenAIResponse
		if err := json.Unmarshal(out, &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Choices) != 1 || response.Choices[0].FinishReason != tc.reason || response.Choices[0].Message.Content != "partial" || response.Usage.TotalTokens != 6 {
			t.Fatalf("wrong completion semantics: %s", out)
		}
		if bytes.Contains(out, []byte(`"error"`)) {
			t.Fatalf("null error converted into failure: %s", out)
		}
	}
}
