package adapter

import (
	"bytes"
	"fmt"
	"testing"
)

func pipeCanonicalStream(down StreamConverter, canonical []byte) []byte {
	var output []byte
	for _, line := range bytes.SplitAfter(canonical, []byte("\n")) {
		output = append(output, down.Convert(line)...)
	}
	return output
}

func TestAnthropicToResponsesStreamTerminationIntegration(t *testing.T) {
	for _, tc := range []struct{ reason, canonicalReason, event string }{
		{"end_turn", "stop", "response.completed"},
		{"stop_sequence", "stop", "response.completed"},
		{"tool_use", "tool_calls", "response.completed"},
		{"max_tokens", "length", "response.incomplete"},
		{"refusal", "content_filter", "response.incomplete"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			up := (&AnthropicAdapter{}).NewStreamConverter()
			down := (&ResponsesDownstreamAdapter{}).NewStreamConverter()
			inputs := []string{
				`data: {"type":"message_start","message":{"id":"msg_a","model":"claude","usage":{"input_tokens":12}}}`,
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
				fmt.Sprintf(`data: {"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":5}}`, tc.reason),
				`data: {"type":"message_stop"}`,
			}
			var output []byte
			for _, input := range inputs {
				output = append(output, pipeCanonicalStream(down, up.Convert([]byte(input)))...)
			}
			canonicalTail := up.Finish()
			if !bytes.Contains(canonicalTail, []byte(fmt.Sprintf(`"finish_reason":%q`, tc.canonicalReason))) {
				t.Fatalf("not canonical: %s", canonicalTail)
			}
			output = append(output, pipeCanonicalStream(down, canonicalTail)...)
			output = append(output, down.Finish()...)
			if !bytes.Contains(output, []byte("event: "+tc.event+"\n")) {
				t.Fatalf("missing %s: %s", tc.event, output)
			}
			if tc.event == "response.incomplete" && bytes.Contains(output, []byte("event: response.completed\n")) {
				t.Fatalf("incomplete became success: %s", output)
			}
			if !bytes.Contains(output, []byte(`"input_tokens":12`)) || !bytes.Contains(output, []byte(`"output_tokens":5`)) {
				t.Fatalf("usage lost: %s", output)
			}
		})
	}
}

func TestGeminiToResponsesStreamTerminationIntegration(t *testing.T) {
	for _, tc := range []struct{ reason, event string }{
		{"STOP", "response.completed"}, {"MAX_TOKENS", "response.incomplete"},
		{"SAFETY", "response.incomplete"}, {"RECITATION", "response.incomplete"},
		{"MALFORMED_FUNCTION_CALL", "error"}, {"UNEXPECTED_NEW_REASON", "error"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			up := (&GeminiAdapter{}).NewStreamConverter()
			down := (&ResponsesDownstreamAdapter{}).NewStreamConverter()
			input := fmt.Sprintf(`data: {"candidates":[{"content":{"parts":[{"text":"partial"}]},"finishReason":%q}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"totalTokenCount":10}}`, tc.reason)
			output := pipeCanonicalStream(down, up.Convert([]byte(input)))
			output = append(output, pipeCanonicalStream(down, up.Finish())...)
			output = append(output, down.Finish()...)
			if !bytes.Contains(output, []byte("event: "+tc.event+"\n")) {
				t.Fatalf("missing %s: %s", tc.event, output)
			}
			if tc.event != "response.completed" && bytes.Contains(output, []byte("event: response.completed\n")) {
				t.Fatalf("non-success became completed: %s", output)
			}
		})
	}
}
