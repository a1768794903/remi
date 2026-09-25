package memories

import "testing"

func TestParseExtractResultAcceptsStrictJSONAndFencedJSON(t *testing.T) {
	for _, raw := range []string{`{"memories":["likes tea"],"profile":"tea drinker"}`, "```json\n{\"memories\":[\"likes tea\"],\"profile\":\"tea drinker\"}\n```"} {
		got, err := ParseExtractResult(raw)
		if err != nil || len(got.Memories) != 1 || got.Profile != "tea drinker" {
			t.Fatalf("got %+v, %v", got, err)
		}
	}
}

func TestParseExtractResultRejectsMalformedOrEmptyMemory(t *testing.T) {
	if _, err := ParseExtractResult(`{"memories":[""]}`); err == nil {
		t.Fatal("empty memories must be rejected")
	}
	if _, err := ParseExtractResult("not json"); err == nil {
		t.Fatal("malformed JSON must be rejected")
	}
}
