package chat

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

type shareClaims struct {
	UID       string   `json:"uid"`
	IDs       []string `json:"ids"`
	ExpiresAt int64    `json:"expires_at"`
}

func makeShareToken(uid string, ids []string, secret string) (string, error) {
	return makeShareTokenAt(uid, ids, secret, time.Now().Add(7*24*time.Hour).Unix())
}

func makeShareTokenAt(uid string, ids []string, secret string, expiresAt int64) (string, error) {
	if uid == "" || secret == "" || len(ids) == 0 {
		return "", errors.New("invalid share claims")
	}
	body, err := json.Marshal(shareClaims{UID: uid, IDs: ids, ExpiresAt: expiresAt})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

func parseShareToken(token, secret string) (string, []string, error) {
	if secret == "" {
		return "", nil, errors.New("share secret is not configured")
	}
	parts := splitToken(token)
	if len(parts) != 2 {
		return "", nil, errors.New("invalid share token")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(expected, actual) {
		return "", nil, errors.New("invalid share token")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", nil, err
	}
	var claims shareClaims
	if json.Unmarshal(body, &claims) != nil || claims.UID == "" || len(claims.IDs) == 0 || claims.ExpiresAt <= time.Now().Unix() {
		return "", nil, errors.New("invalid share claims")
	}
	return claims.UID, claims.IDs, nil
}

func splitToken(token string) []string {
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			return []string{token[:i], token[i+1:]}
		}
	}
	return nil
}
