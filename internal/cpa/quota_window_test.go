package cpa

import "testing"

func TestParseCodexUsageClassifiesPrimarySevenDayWindowByDuration(t *testing.T) {
	payload := []byte(`{
		"plan_type":"plus",
		"rate_limit":{
			"primary_window":{"used_percent":68,"reset_at":1760000000,"reset_after_seconds":423997,"limit_window_seconds":604800}
		}
	}`)
	got, err := parseCodexUsage(payload, AuthFile{AuthIndex: "auth-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.FiveHour != nil {
		t.Fatalf("7d window must not be labeled as 5h: %+v", got.FiveHour)
	}
	if got.Weekly == nil || got.Weekly.RemainingPercent == nil || *got.Weekly.RemainingPercent != 32 {
		t.Fatalf("7d window mismatch: %+v", got.Weekly)
	}
}

func TestParseCodexUsageClassifiesBothWindowsByDuration(t *testing.T) {
	payload := []byte(`{
		"rate_limit":{
			"primary_window":{"used_percent":20,"limit_window_seconds":18000},
			"secondary_window":{"used_percent":40,"limit_window_seconds":604800}
		}
	}`)
	got, err := parseCodexUsage(payload, AuthFile{AuthIndex: "auth-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.FiveHour == nil || got.Weekly == nil {
		t.Fatalf("both windows should be preserved: %+v", got)
	}
}
