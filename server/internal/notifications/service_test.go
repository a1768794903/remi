package notifications

import "testing"

func TestDailySummaryDefaultsMatchPythonContract(t *testing.T) {
	settings := DailySummarySettings{Enabled: true, Hour: 22}
	if !settings.Enabled || settings.Hour != 22 {
		t.Fatalf("defaults = %#v", settings)
	}
	if validateDailySummaryHour(0) != nil || validateDailySummaryHour(23) != nil {
		t.Fatal("boundary hours must be accepted")
	}
	if validateDailySummaryHour(-1) == nil || validateDailySummaryHour(24) == nil {
		t.Fatal("out-of-range hours must be rejected")
	}
}

func TestMentorFrequencyRangeMatchesPythonContract(t *testing.T) {
	for _, value := range []int{0, 5} {
		if validateMentorFrequency(value) != nil {
			t.Fatalf("frequency %d should be accepted", value)
		}
	}
	for _, value := range []int{-1, 6} {
		if validateMentorFrequency(value) == nil {
			t.Fatalf("frequency %d should be rejected", value)
		}
	}
}
