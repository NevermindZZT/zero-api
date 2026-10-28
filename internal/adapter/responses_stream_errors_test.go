package adapter

import (
	"bytes"
	"testing"
)

func TestResponsesUpstreamUnknownToolCannotAttachToAnotherCall(t *testing.T) {
	c := (&ResponsesAdapter{}).NewStreamConverter()
	c.Convert([]byte(`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_known","call_id":"call_known","name":"read"}}`))
	out := c.Convert([]byte(`data: {"type":"response.function_call_arguments.delta","item_id":"fc_unknown","output_index":0,"delta":"{}"}`))
	if !bytes.Contains(out, []byte(`"error"`)) || c.Finish() != nil {
		t.Fatalf("unknown item was not rejected: %s", out)
	}
}

func TestResponsesUpstreamMissingCallIdentityCannotComplete(t *testing.T) {
	c := (&ResponsesAdapter{}).NewStreamConverter()
	out := c.Convert([]byte(`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","name":"read"}}`))
	c.Convert([]byte(`data: {"type":"response.completed","response":{"status":"completed"}}`))
	if !bytes.Contains(out, []byte(`"error"`)) || c.Finish() != nil {
		t.Fatalf("missing call_id became success: %s", out)
	}
}

func TestResponsesDownstreamMissingToolIdentityCannotComplete(t *testing.T) {
	c := (&ResponsesDownstreamAdapter{}).NewStreamConverter()
	c.Convert([]byte(`data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}`))
	c.Convert([]byte("data: [DONE]"))
	out := c.Finish()
	if !bytes.Contains(out, []byte("event: error")) || bytes.Contains(out, []byte("event: response.completed")) {
		t.Fatalf("missing tool identity became success: %s", out)
	}
}

func TestResponsesDownstreamBuffersArgumentsUntilIdentity(t *testing.T) {
	c := (&ResponsesDownstreamAdapter{}).NewStreamConverter()
	first := c.Convert([]byte(`data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{"}}]}}]}`))
	second := c.Convert([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_late","function":{"name":"read","arguments":"}"}}]},"finish_reason":"tool_calls"}]}`))
	tail := c.Finish()
	if bytes.Contains(first, []byte("response.function_call_arguments.delta")) || !bytes.Contains(second, []byte(`"delta":"{}"`)) || !bytes.Contains(tail, []byte(`"call_id":"call_late"`)) || !bytes.Contains(tail, []byte(`"arguments":"{}"`)) {
		t.Fatalf("late identity lost buffered args: %s %s %s", first, second, tail)
	}
}
