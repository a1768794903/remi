package usage

import "testing"

func TestCostConversion(t *testing.T) {
	got := Snapshot{CostUSD: float64(int64(1250000)) / 1_000_000}
	if got.CostUSD != 1.25 {
		t.Fatalf("cost conversion = %v", got.CostUSD)
	}
}
