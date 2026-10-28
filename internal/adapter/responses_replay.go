package adapter

// NormalizeResponsesRequest is retained for compatibility with older callers.
// Native Responses state belongs to the upstream: never rewrite, cache, or
// replay input, previous_response_id, reasoning, or tool-call items locally.
func NormalizeResponsesRequest(body []byte) []byte { return body }

// CaptureResponsesReplay is a compatibility no-op. Responses history must not
// leak across channels, sessions, or API keys through a process-global cache.
func CaptureResponsesReplay(_ []byte) {}
