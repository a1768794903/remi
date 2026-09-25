package payments

import "testing"

func TestStripeSignatureRejectsStaleTimestamp(t *testing.T) {
	if verifyStripe([]byte(`{}`), "t=1,v1=bad", "secret") {
		t.Fatal("stale signature must be rejected")
	}
}
