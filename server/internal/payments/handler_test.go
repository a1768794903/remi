package payments

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestVerifyStripeSignature(t *testing.T) {
	payload := []byte(`{"type":"checkout.session.completed"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte("whsec_test"))
	_, _ = mac.Write([]byte(ts + "." + string(payload)))
	header := "t=" + ts + ",v1=" + fmt.Sprintf("%x", mac.Sum(nil))
	if !verifyStripe(payload, header, "whsec_test") {
		t.Fatal("expected valid Stripe signature")
	}
	if verifyStripe(payload, header+"bad", "whsec_test") {
		t.Fatal("expected invalid Stripe signature")
	}
}
