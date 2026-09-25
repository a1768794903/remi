package desktopprompts

import "testing"

func TestMatchesAudience(t *testing.T) {
	audience := map[string]any{"channels": []any{"stable"}, "min_build": float64(10), "rollout_pct": float64(100)}
	if !matchesAudience(audience, "uid", "prompt", "stable", 10) {
		t.Fatal("expected matching audience")
	}
	if matchesAudience(audience, "uid", "prompt", "beta", 10) {
		t.Fatal("expected channel rejection")
	}
	if matchesAudience(audience, "uid", "prompt", "stable", 9) {
		t.Fatal("expected build rejection")
	}
}
