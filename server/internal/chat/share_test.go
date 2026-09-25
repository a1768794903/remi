package chat

import "testing"

func TestShareTokenRoundTrip(t *testing.T) {
	token, err := makeShareToken("user-1", []string{"m1", "m2"}, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	uid, ids, err := parseShareToken(token, "test-secret")
	if err != nil || uid != "user-1" || len(ids) != 2 || ids[1] != "m2" {
		t.Fatalf("round trip failed: %q %v %v", uid, ids, err)
	}
}

func TestShareTokenRejectsTampering(t *testing.T) {
	token, _ := makeShareToken("user-1", []string{"m1"}, "test-secret")
	if _, _, err := parseShareToken(token+"x", "test-secret"); err == nil {
		t.Fatal("tampered token accepted")
	}
}

func TestShareTokenRejectsExpiredClaims(t *testing.T) {
	token, err := makeShareTokenAt("user-1", []string{"m1"}, "test-secret", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := parseShareToken(token, "test-secret"); err == nil {
		t.Fatal("expired token accepted")
	}
}
