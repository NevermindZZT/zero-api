package cpa

import "testing"

func TestParseAntigravityQuota(t *testing.T) {
	got, err := parseAntigravityQuota([]byte(`{"paidTier":{"id":"tier-1","availableCredits":[{"creditType":"GOOGLE_ONE_AI","creditAmount":"25000","minimumCreditAmountForUsage":"50"}]}}`), AuthFile{AuthIndex: "ag-1", Email: "user@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "antigravity" || got.AICredits == nil || *got.AICredits != 25000 {
		t.Fatalf("unexpected credits: %#v", got)
	}
	if got.AICreditsMinimum == nil || *got.AICreditsMinimum != 50 {
		t.Fatalf("unexpected minimum credits: %#v", got.AICreditsMinimum)
	}
}

func TestParseAntigravityQuotaAcceptsEmptyCreditAmountAsZero(t *testing.T) {
	got, err := parseAntigravityQuota([]byte(`{"paidTier":{"availableCredits":[{"creditType":"GOOGLE_ONE_AI","creditAmount":"","minimumCreditAmountForUsage":"50"}]}}`), AuthFile{AuthIndex: "ag-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AICredits == nil || *got.AICredits != 0 {
		t.Fatalf("credits = %#v, want zero", got.AICredits)
	}
}

func TestParseAntigravityQuotaRejectsMissingGoogleOneCredits(t *testing.T) {
	if _, err := parseAntigravityQuota([]byte(`{"paidTier":{"availableCredits":[{"creditType":"OTHER","creditAmount":"1"}]}}`), AuthFile{AuthIndex: "ag-1"}); err == nil {
		t.Fatal("expected missing GOOGLE_ONE_AI credits error")
	}
}
