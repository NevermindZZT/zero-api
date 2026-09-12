package cpa

import (
	"math"
	"testing"
)

func TestParseAntigravityQuotaSummary(t *testing.T) {
	body := []byte(`{"groups":[{"displayName":"Gemini Models","description":"Models within this group: Gemini Flash, Gemini Pro","buckets":[{"bucketId":"gemini-5h","displayName":"Five Hour Limit Remaining","window":"5h","resetTime":"2026-09-12T06:19:10Z","remainingFraction":0.726},{"bucketId":"gemini-weekly","displayName":"Weekly Limit Remaining","window":"weekly","resetTime":"2026-09-19T01:19:10Z","remainingFraction":"0.946"}]},{"displayName":"Claude and GPT models","buckets":[{"bucketId":"3p-5h","displayName":"Five Hour Limit Remaining","window":"5h","remainingFraction":1}]}]}`)
	got, err := parseAntigravityQuotaSummary(body, AuthFile{AuthIndex: "ag-1", Email: "user@example.com", PlanType: "pro"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "antigravity" || len(got.AntigravityGroups) != 2 {
		t.Fatalf("unexpected snapshot: %#v", got)
	}
	gemini := got.AntigravityGroups[0]
	if gemini.Label != "Gemini Models" || len(gemini.Buckets) != 2 {
		t.Fatalf("unexpected Gemini group: %#v", gemini)
	}
	if math.Abs(gemini.Buckets[0].RemainingPercent-72.6) > 0.001 || gemini.Buckets[0].ResetAt == nil {
		t.Fatalf("unexpected 5h bucket: %#v", gemini.Buckets[0])
	}
	if math.Abs(gemini.Buckets[1].RemainingPercent-94.6) > 0.001 {
		t.Fatalf("unexpected weekly bucket: %#v", gemini.Buckets[1])
	}
}

func TestParseAntigravityQuotaSummaryRejectsEmptyGroups(t *testing.T) {
	if _, err := parseAntigravityQuotaSummary([]byte(`{"groups":[]}`), AuthFile{AuthIndex: "ag-1"}); err == nil {
		t.Fatal("expected empty groups error")
	}
}

func TestAntigravityMatchAcceptsTypeFallbackAndRejectsDisabled(t *testing.T) {
	auth := authFileFromMap(map[string]any{"type": "antigravity", "auth_index": "ag-1", "project_id": "project"})
	if !(AntigravityQuotaProvider{}).Match(auth) {
		t.Fatalf("expected type fallback to match: %#v", auth)
	}
	auth.Disabled = true
	if (AntigravityQuotaProvider{}).Match(auth) {
		t.Fatal("disabled auth must not match")
	}
}
