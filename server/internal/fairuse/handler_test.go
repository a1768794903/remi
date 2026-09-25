package fairuse

import (
	"strings"
	"testing"
)

func TestFairUsePercentClamps(t *testing.T) {
	if got := pct(150, 100); got != 100 {
		t.Fatalf("got=%v", got)
	}
	if got := pct(25, 100); got != 25 {
		t.Fatalf("got=%v", got)
	}
	if got := pct(1, 0); got != 0 {
		t.Fatalf("got=%v", got)
	}
}

func TestFairUseMessageIncludesCaseReference(t *testing.T) {
	if got := message("restrict", "case-1"); got == "" || !strings.Contains(got, "case-1") {
		t.Fatalf("message=%q", got)
	}
}
