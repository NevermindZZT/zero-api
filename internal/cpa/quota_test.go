package cpa

import "testing"

func TestParseCodexUsage(t *testing.T) {
	payload := []byte(`{
		"plan_type":"plus",
		"rate_limit":{
			"primary_window":{"used_percent":23.5,"reset_at":1760000000,"reset_after_seconds":10240,"limit_window_seconds":18000},
			"secondary_window":{"used_percent":41.2,"reset_at":1760500000,"reset_after_seconds":432000,"limit_window_seconds":604800},
			"allowed":true,"limit_reached":false
		},
		"rate_limit_reset_credits":{"available_count":2}
	}`)
	got, err := parseCodexUsage(payload, AuthFile{AuthIndex: "auth-1", AccountID: "acct-1", Email: "user@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "codex" || got.PlanType != "plus" || got.Status != "available" {
		t.Fatalf("metadata mismatch: %+v", got)
	}
	if got.FiveHour == nil || got.FiveHour.RemainingPercent == nil || *got.FiveHour.RemainingPercent != 76.5 || got.FiveHour.ResetAfterSeconds == nil || *got.FiveHour.ResetAfterSeconds != 10240 {
		t.Fatalf("5h mismatch: %+v", got.FiveHour)
	}
	if got.Weekly == nil || got.Weekly.RemainingPercent == nil || *got.Weekly.RemainingPercent != 58.8 || got.Weekly.ResetAfterSeconds == nil || *got.Weekly.ResetAfterSeconds != 432000 {
		t.Fatalf("7d mismatch: %+v", got.Weekly)
	}
	if got.ResetCredits != 2 {
		t.Fatalf("reset credits = %d, want 2", got.ResetCredits)
	}
}

func TestParseCodexUsageRejectsInvalidPayload(t *testing.T) {
	if _, err := parseCodexUsage([]byte(`<html>challenge</html>`), AuthFile{AuthIndex: "auth-1"}); err == nil {
		t.Fatal("expected invalid payload error")
	}
}
