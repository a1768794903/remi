package memories

import "testing"

func TestNormalizeMemoryUseActionMatchesPythonContract(t *testing.T) {
	got, err := NormalizeMemoryUseAction(" USEFUL ")
	if err != nil || got != MemoryUseUseful {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := NormalizeMemoryUseAction("unknown"); err == nil {
		t.Fatal("invalid action should fail")
	}
}

func TestBuildMemoryUseStatePreservesSuppressionAndDetectsFeedbackConflict(t *testing.T) {
	state, weight, err := BuildMemoryUseState(MemoryUseState{Suppressed: true}, MemoryUseUseful, "f-1")
	if err != nil {
		t.Fatal(err)
	}
	if !state.Suppressed || state.State != "suppressed" || weight != 1 {
		t.Fatalf("unexpected useful state: %+v weight=%d", state, weight)
	}
	_, _, err = BuildMemoryUseState(state, MemoryUseAllow, "f-1")
	if err != ErrFeedbackConflict {
		t.Fatalf("expected feedback conflict, got %v", err)
	}
}

func TestNormalizeFeedbackIDUsesRuneLimit(t *testing.T) {
	if _, err := NormalizeFeedbackID(""); err == nil {
		t.Fatal("blank feedback id should fail")
	}
	if _, err := NormalizeFeedbackID("x" + string(make([]rune, MaxFeedbackIDLength))); err == nil {
		t.Fatal("oversized feedback id should fail")
	}
}
