package chat

import "testing"

func TestTrimTextRejectsEmptyAndOversizedMessages(t *testing.T) {
	if _, err := trimText(" \t"); err == nil {
		t.Fatal("expected empty text error")
	}
	if _, err := trimText(string(make([]byte, 100001))); err == nil {
		t.Fatal("expected oversized text error")
	}
	got, err := trimText("  hello  ")
	if err != nil || got != "hello" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestValidateSenderMatchesPythonContract(t *testing.T) {
	for _, sender := range []string{"human", "ai"} {
		if !validateSender(sender) {
			t.Fatalf("sender %q rejected", sender)
		}
	}
	if validateSender("system") {
		t.Fatal("system sender should be rejected")
	}
}
