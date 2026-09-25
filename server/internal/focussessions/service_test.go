package focussessions

import "testing"

func TestStatsUsesSixtySecondDefaultForDistractedSession(t *testing.T) {
	got := stats("2026-09-25", []session{{Status: "focused", Duration: 120}, {Status: "distracted", App: "chat", Duration: 0}, {Status: "distracted", App: "chat", Duration: 90}})
	if got.FocusedMinutes != 2 || got.DistractedMinutes != 2 || got.SessionCount != 3 || got.Top[0].TotalSeconds != 150 || got.Top[0].Count != 2 {
		t.Fatalf("unexpected stats: %#v", got)
	}
}

func TestStatsTopDistractionsIsLimitedToFive(t *testing.T) {
	items := make([]session, 0, 6)
	for i := 0; i < 6; i++ {
		items = append(items, session{Status: "distracted", App: string(rune('a' + i)), Duration: 60})
	}
	if got := stats("2026-09-25", items); len(got.Top) != 5 {
		t.Fatalf("got %d top distractions", len(got.Top))
	}
}
