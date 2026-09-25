package scores

import "testing"

func TestScorePeriodRoundsToOneDecimalAndHandlesEmpty(t *testing.T) {
	if got := scorePeriod(0, 0); got.Score != 0 || got.TotalTasks != 0 {
		t.Fatalf("empty score: %#v", got)
	}
	if got := scorePeriod(1, 3); got.Score != 33.3 || got.CompletedTasks != 1 || got.TotalTasks != 3 {
		t.Fatalf("rounded score: %#v", got)
	}
}
