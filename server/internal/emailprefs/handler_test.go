package emailprefs

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http/httptest"
	"testing"
)

func TestVerifyLifecycleToken(t *testing.T) {
	t.Setenv("LIFECYCLE_EMAIL_SIGNING_SECRET", "secret")
	uid := "user-123"
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(uid + ":lifecycle"))
	token := base64.RawURLEncoding.EncodeToString([]byte(uid)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if got, ok := verify(token); !ok || got != uid {
		t.Fatalf("token did not verify: %q %v", got, ok)
	}
	if _, ok := verify(token + "x"); ok {
		t.Fatal("tampered token verified")
	}
}

func TestGetUnsubscribeNeverWrites(t *testing.T) {
	t.Setenv("LIFECYCLE_EMAIL_SIGNING_SECRET", "")
	r := httptest.NewRequest("GET", "/email/unsubscribe?token=bad", nil)
	w := httptest.NewRecorder()
	(Handler{}).ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("status=%d", w.Code)
	}
}
