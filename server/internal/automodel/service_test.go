package automodel

import "testing"

func TestScoreClampsAndWeightsQualityAndSpeed(t *testing.T) {
	if got := score(100, 250); got != 1 {
		t.Fatalf("score = %v", got)
	}
	if got := score(-1, -1); got != 0 {
		t.Fatalf("score = %v", got)
	}
}

func TestPickProviderUsesHighestScore(t *testing.T) {
	got, detail := pick(map[string]modelMetrics{
		"geminiFlashLive": {Quality: 80, Speed: 100},
		"gptRealtime2":    {Quality: 90, Speed: 200},
	})
	if got != "gptRealtime2" {
		t.Fatalf("provider = %q", got)
	}
	if detail["scores"] == nil {
		t.Fatal("missing scores")
	}
}

func TestPickDefaultsWhenNoMatchingModels(t *testing.T) {
	got, detail := pick(map[string]modelMetrics{})
	if got != "geminiFlashLive" || detail["reason"] == nil {
		t.Fatalf("fallback = %q %#v", got, detail)
	}
}
