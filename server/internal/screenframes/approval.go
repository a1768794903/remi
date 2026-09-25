package screenframes

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
)

type approvalClaims struct {
	UID             string `json:"uid"`
	Purpose         string `json:"purpose"`
	SubjectID       string `json:"subject_id"`
	CanonicalSHA256 string `json:"canonical_sha256"`
	IssuedAt        int64  `json:"issued_at"`
	ExpiresAt       int64  `json:"expires_at"`
}

func mintApproval(uid, purpose, subjectID string, canonical []byte, now time.Time) (string, error) {
	secret := []byte(strings.TrimSpace(os.Getenv("SCREEN_FRAME_APPROVAL_SECRET")))
	if len(secret) < 32 {
		return "", errors.New("approval secret is not configured")
	}
	sum := sha256.Sum256(canonical)
	claims := approvalClaims{UID: uid, Purpose: purpose, SubjectID: subjectID, CanonicalSHA256: base64.RawURLEncoding.EncodeToString(sum[:]), IssuedAt: now.Unix(), ExpiresAt: now.Add(10 * time.Minute).Unix()}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyApproval(token, uid, purpose, subjectID string, canonical []byte, now time.Time) error {
	secret := []byte(strings.TrimSpace(os.Getenv("SCREEN_FRAME_APPROVAL_SECRET")))
	if len(secret) < 32 {
		return errors.New("approval secret is not configured")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return errors.New("invalid approval token")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return errors.New("invalid approval signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errors.New("invalid approval payload")
	}
	var claims approvalClaims
	if json.Unmarshal(payload, &claims) != nil {
		return errors.New("invalid approval claims")
	}
	if claims.UID != uid || claims.Purpose != purpose || claims.SubjectID != subjectID || claims.ExpiresAt <= now.Unix() || claims.IssuedAt > now.Add(time.Minute).Unix() {
		return errors.New("approval claims rejected")
	}
	sum := sha256.Sum256(canonical)
	if !hmac.Equal([]byte(claims.CanonicalSHA256), []byte(base64.RawURLEncoding.EncodeToString(sum[:]))) {
		return errors.New("approval digest mismatch")
	}
	return nil
}
