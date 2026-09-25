package dailysummaries

import "testing"

func TestValidateVisibility(t *testing.T) {
	if !validVisibility("shared") || !validVisibility("private") || validVisibility("public") {
		t.Fatal("visibility validation does not match Python contract")
	}
}

func TestPublicSummaryFields(t *testing.T) {
	in := map[string]any{"id": "s1", "date": "2026-09-25", "visibility": "shared", "headline": "h", "private": "secret", "user_id": "u"}
	out := publicFields(in)
	if out["headline"] != "h" || out["private"] != nil || out["user_id"] != nil || out["visibility"] != nil {
		t.Fatalf("private fields leaked: %#v", out)
	}
}
