package integrations

import (
	"testing"
	"time"
)

func TestXPKCEChallengeIsDerivedFromVerifier(t *testing.T) {
	verifier, challenge, err := xPKCE()
	if err != nil || verifier == "" || challenge == "" {
		t.Fatalf("verifier=%q challenge=%q err=%v", verifier, challenge, err)
	}
	if challenge == verifier {
		t.Fatal("challenge must not equal verifier")
	}
}

func TestXPostKindValidationContract(t *testing.T) {
	for _, kind := range []string{"tweet", "bookmark", "like"} {
		if !validXPostKind(kind) {
			t.Fatalf("%s should be valid", kind)
		}
	}
	if validXPostKind("retweet") {
		t.Fatal("unsupported kind should be rejected")
	}
}

func TestXExpiryRejectsMalformedValues(t *testing.T) {
	if !xExpiry("not-a-time").IsZero() {
		t.Fatal("malformed expiry should be zero")
	}
	if xExpiry(time.Now().UTC().Format(time.RFC3339)).IsZero() {
		t.Fatal("valid expiry should parse")
	}
}
