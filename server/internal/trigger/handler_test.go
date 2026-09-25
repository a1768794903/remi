package trigger

import (
	"encoding/binary"
	"testing"
)

func TestParseFrameUsesLittleEndianProtocol(t *testing.T) {
	frame := make([]byte, 12)
	binary.LittleEndian.PutUint32(frame, 101)
	if kind, payload, err := parseFrame(frame); err != nil || kind != 101 || len(payload) != 8 {
		t.Fatalf("kind=%d payload=%d err=%v", kind, len(payload), err)
	}
}

func TestParseFrameRejectsMalformedFrames(t *testing.T) {
	for _, frame := range [][]byte{{}, {1, 2, 3}, {9, 0, 0, 0}, {101, 0, 0, 0}} {
		if _, _, err := parseFrame(frame); err == nil {
			t.Fatalf("expected rejection for %v", frame)
		}
	}
}

func TestSafeWebhookURLRejectsPrivateLiteralTargets(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/hook", "http://10.0.0.2/hook", "file:///tmp/hook", "not-a-url"} {
		if safeWebhookURL(raw) {
			t.Fatalf("expected unsafe URL: %s", raw)
		}
	}
	if !safeWebhookURL("https://example.com/hook") {
		t.Fatal("expected public HTTPS URL")
	}
}
