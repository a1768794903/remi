package screenactivity

import "testing"

func TestNormalizeTimestampUsesUTCMilliseconds(t *testing.T) {
	got, err := normalizeTimestamp("2026-09-25T01:02:03.123456+08:00", false)
	if err != nil || got != "2026-09-24 17:02:03.123" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestSummaryUsesLookaheadForCoverageAndLimitsTitles(t *testing.T) {
	rows := make([]row, 0, 3)
	for i := 0; i < 3; i++ {
		rows = append(rows, row{ID: string(rune('a' + i)), App: "Editor", Title: "Window", Timestamp: "2026-09-25 01:00:00.000"})
	}
	got := summarize(rows, 2)
	if !got.Truncated || got.Total != 2 || got.Apps["Editor"].Count != 2 {
		t.Fatalf("unexpected summary: %#v", got)
	}
}
