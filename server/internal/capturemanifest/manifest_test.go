package capturemanifest

import (
	"bytes"
	"testing"
	"time"
)

func TestIssueAndVerifyBindsIdentityAndFiles(t *testing.T) {
	t.Setenv("SYNC_CONTENT_ID_SECRET", "test-secret")
	now := time.Unix(1000, 0)
	token, err := Issue("uid-1", "device-1", "conv-1", []Claim{{Name: "b.opus", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, {Name: "a.opus", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	claims, ok := Verify(token, "uid-1", "device-1", "conv-1", []string{"a.opus", "b.opus"}, now.Add(time.Minute))
	if !ok || len(claims) != 2 || claims[0].Name != "a.opus" {
		t.Fatalf("unexpected verified claims: %#v, ok=%v", claims, ok)
	}
	if _, ok := Verify(token, "uid-2", "device-1", "conv-1", []string{"a.opus", "b.opus"}, now.Add(time.Minute)); ok {
		t.Fatal("manifest verified for a different user")
	}
}

func TestVerifyRejectsExpiredAndFilenameMismatch(t *testing.T) {
	t.Setenv("SYNC_CONTENT_ID_SECRET", "test-secret")
	now := time.Unix(1000, 0)
	token, err := Issue("uid-1", "device-1", "conv-1", []Claim{{Name: "a.opus", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := Verify(token, "uid-1", "device-1", "conv-1", []string{"a.opus"}, now.Add(16*time.Minute)); ok {
		t.Fatal("expired manifest verified")
	}
	if _, ok := Verify(token, "uid-1", "device-1", "conv-1", []string{"other.opus"}, now.Add(time.Minute)); ok {
		t.Fatal("manifest verified with different filenames")
	}
}

func TestClaimsMatchBytes(t *testing.T) {
	claims := []Claim{{Name: "a.opus", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	if ClaimsMatchBytes(claims, map[string][]byte{"a.opus": bytes.Repeat([]byte{0xAA}, 32)}) {
		t.Fatal("unexpectedly accepted bytes with a different digest")
	}
}
