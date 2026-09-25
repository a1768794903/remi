package users

import "testing"

func TestChatQuotaLimitForPlan(t *testing.T) {
	cases := map[string]struct {
		unit  string
		limit *int64
	}{
		"basic":     {unit: "questions", limit: ptrQuota(30)},
		"operator":  {unit: "questions", limit: ptrQuota(500)},
		"architect": {unit: "cost_usd", limit: ptrQuota(400)},
		"neo":       {unit: "questions", limit: ptrQuota(200)},
	}
	for plan, want := range cases {
		gotUnit, gotLimit := chatQuotaSpec(plan)
		if gotUnit != want.unit {
			t.Fatalf("%s unit = %q, want %q", plan, gotUnit, want.unit)
		}
		if gotLimit == nil || *gotLimit != *want.limit {
			t.Fatalf("%s limit = %v, want %v", plan, gotLimit, *want.limit)
		}
	}
}

func ptrQuota(value int64) *int64 { return &value }

func TestChatQuotaPercentIsClamped(t *testing.T) {
	if got := chatQuotaPercent(40, ptrQuota(30)); got != 100 {
		t.Fatalf("overage percent = %v", got)
	}
	if got := chatQuotaPercent(15, ptrQuota(30)); got != 50 {
		t.Fatalf("half percent = %v", got)
	}
	if got := chatQuotaPercent(15, nil); got != 0 {
		t.Fatalf("unlimited percent = %v", got)
	}
}

func TestChatQuotaArchitectUsesCostDollars(t *testing.T) {
	unit, limit := chatQuotaSpec("architect")
	if unit != "cost_usd" || limit == nil || *limit != 400 {
		t.Fatalf("architect quota = (%q, %v), want (cost_usd, 400)", unit, limit)
	}
	if got := chatQuotaPercent(200, limit); got != 50 {
		t.Fatalf("architect cost percent = %v, want 50", got)
	}
}
