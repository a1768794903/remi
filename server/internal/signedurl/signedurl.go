package signedurl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func Build(path, uid string, expiry time.Time) (string, error) {
	secret := []byte(strings.TrimSpace(os.Getenv("SCREEN_FRAME_URL_SECRET")))
	if len(secret) < 32 {
		return "", errors.New("signed URL secret is not configured")
	}
	exp := strconv.FormatInt(expiry.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(uid + "\n" + path + "\n" + exp))
	return path + "?expires=" + url.QueryEscape(exp) + "&sig=" + url.QueryEscape(base64.RawURLEncoding.EncodeToString(mac.Sum(nil))), nil
}

func Verify(path, uid string, values url.Values, now time.Time) bool {
	secret := []byte(strings.TrimSpace(os.Getenv("SCREEN_FRAME_URL_SECRET")))
	if len(secret) < 32 {
		return false
	}
	exp := values.Get("expires")
	timestamp, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || timestamp <= now.Unix() {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(values.Get("sig"))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(uid + "\n" + path + "\n" + exp))
	return hmac.Equal(signature, mac.Sum(nil))
}
