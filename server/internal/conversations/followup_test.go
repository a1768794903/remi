package conversations

import "testing"

func TestBuildFollowupPromptRequiresEnoughTranscriptWords(t *testing.T) {
	if got := buildFollowupPrompt([]string{"hello", "there"}); got != "" {
		t.Fatalf("short transcript returned %q", got)
	}
	words := make([]string, 0, 110)
	for i := 0; i < 110; i++ {
		words = append(words, "word")
	}
	got := buildFollowupPrompt(words)
	if got == "" {
		t.Fatal("long transcript produced empty prompt")
	}
	if len(got) < len("word ")*90 {
		t.Fatalf("prompt was not built from the bounded transcript: len=%d", len(got))
	}
}
