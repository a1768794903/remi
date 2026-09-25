package finalization

import "testing"

func TestParseStructuredResultRejectsNonJSONAndAcceptsBoundedResult(t *testing.T) {
	result, err := ParseResult(`{"summary":"A useful summary","memories":[{"content":"User prefers tea"}],"action_items":[{"description":"Buy tea"}]}`)
	if err != nil || result.Summary != "A useful summary" || len(result.Memories) != 1 || len(result.ActionItems) != 1 {
		t.Fatalf("unexpected result: %#v err=%v", result, err)
	}
	if _, err := ParseResult("not json"); err == nil {
		t.Fatal("accepted non-JSON provider output")
	}
}

func TestBuildTranscriptPromptIncludesSpeakerAndText(t *testing.T) {
	prompt := BuildTranscriptPrompt([]TranscriptLine{{Speaker: "Alice", Text: "Hello"}, {Speaker: "Bob", Text: "World"}})
	if prompt == "" || !contains(prompt, "Alice: Hello") || !contains(prompt, "Bob: World") {
		t.Fatalf("prompt omitted transcript lines: %q", prompt)
	}
}
