package csat

import "testing"

func TestNormalizeConfigClampsValues(t *testing.T) {
	got := normalizeConfig(map[string]any{"enabled": false, "title": "  Title ", "question_threshold": 99, "comment_max_score": 0, "revision": -1})
	if got.Title != "Title" || got.QuestionThreshold != 50 || got.CommentMaxScore != 1 || got.Enabled {
		t.Fatalf("config = %#v", got)
	}
}

func TestValidateRating(t *testing.T) {
	if err := validateRating("windows", 5, 0); err != nil {
		t.Fatal(err)
	}
	if validateRating("web", 5, 0) == nil || validateRating("windows", 0, 0) == nil || validateRating("windows", 5, -1) == nil {
		t.Fatal("invalid rating accepted")
	}
}
