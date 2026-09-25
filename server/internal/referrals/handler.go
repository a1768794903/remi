package referrals

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"remi/server/internal/auth"
)

type Handler struct{ DB *sql.DB }

const trialDays = 30

func secret() ([]byte, error) {
	s := os.Getenv("ENCRYPTION_SECRET")
	if len(s) < 32 {
		return nil, errors.New("missing_referral_signing_secret")
	}
	return []byte(s), nil
}
func enc(b []byte) string { return strings.TrimRight(base64.RawURLEncoding.EncodeToString(b), "=") }
func codeFor(uid string) (string, error) {
	if uid == "" || len(uid) > 128 {
		return "", errors.New("invalid_referrer_uid")
	}
	sec, e := secret()
	if e != nil {
		return "", e
	}
	signed := "ref1." + enc([]byte(uid))
	mac := hmac.New(sha256.New, sec)
	_, _ = mac.Write([]byte("omi-desktop-referral:" + signed))
	return signed + "." + enc(mac.Sum(nil)), nil
}
func uidFromCode(code string) (string, error) {
	p := strings.Split(code, ".")
	if len(p) != 3 || p[0] != "ref1" {
		return "", errors.New("malformed_referral_code")
	}
	sec, e := secret()
	if e != nil {
		return "", e
	}
	signed := p[0] + "." + p[1]
	mac := hmac.New(sha256.New, sec)
	_, _ = mac.Write([]byte("omi-desktop-referral:" + signed))
	if !hmac.Equal([]byte(enc(mac.Sum(nil))), []byte(p[2])) {
		return "", errors.New("invalid_referral_signature")
	}
	raw, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil || len(raw) == 0 {
		return "", errors.New("malformed_referrer_uid")
	}
	return string(raw), nil
}
func (h Handler) Link(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	code, e := codeFor(uid)
	if e != nil {
		http.Error(w, "Referral links are temporarily unavailable", 503)
		return
	}
	base := strings.TrimRight(os.Getenv("REFERRAL_PUBLIC_BASE_URL"), "/")
	if base == "" {
		base = "https://omi.me"
	}
	json.NewEncoder(w).Encode(map[string]string{"referral_url": base + "/r/" + code})
}
func (h Handler) Capture(w http.ResponseWriter, r *http.Request) {
	ref, e := uidFromCode(r.PathValue("code"))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	_ = ref
	code := r.PathValue("code")
	target := "https://app.omi.me/login?referral=" + url.QueryEscape(code)
	if base := os.Getenv("REFERRAL_PUBLIC_BASE_URL"); strings.Contains(base, "api.omiapi.com") {
		target += "&environment=dev"
	} else {
		target += "&environment=prod"
	}
	http.Redirect(w, r, target, http.StatusFound)
}
func (h Handler) Claim(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	if h.DB == nil {
		http.Error(w, "referral storage is not configured", 503)
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	ref, e := uidFromCode(in.Code)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	if ref == uid {
		json.NewEncoder(w).Encode(map[string]any{"claimed": false, "trial_days": trialDays})
		return
	}
	var created time.Time
	if e = h.DB.QueryRowContext(r.Context(), `SELECT created_at FROM users WHERE external_uid=?`, uid).Scan(&created); e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	if time.Since(created) > 15*time.Minute || time.Since(created) < 0 {
		json.NewEncoder(w).Encode(map[string]any{"claimed": false, "trial_days": trialDays})
		return
	}
	var currentPlan, currentStatus string
	if subErr := h.DB.QueryRowContext(r.Context(), `SELECT plan,status FROM subscriptions WHERE user_external_uid=?`, uid).Scan(&currentPlan, &currentStatus); subErr == nil {
		paid := map[string]bool{"operator": true, "architect": true, "unlimited": true, "plus": true, "unlimited_v2": true, "pro": true}
		if paid[currentPlan] && currentStatus != "canceled" {
			json.NewEncoder(w).Encode(map[string]any{"claimed": false, "trial_days": trialDays})
			return
		}
	}
	var existing int
	_ = h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM referral_claims WHERE referred_uid=?`, uid).Scan(&existing)
	if existing > 0 {
		json.NewEncoder(w).Encode(map[string]any{"claimed": false, "trial_days": trialDays})
		return
	}
	now := time.Now().UTC()
	end := now.AddDate(0, 0, trialDays)
	_, e = h.DB.ExecContext(r.Context(), `INSERT INTO referral_claims(referred_uid,referrer_uid,program,claimed_at,trial_ends_at) VALUES(?,?,?,?,?)`, uid, ref, "desktop_operator_month_v1", now, end)
	if e != nil {
		if strings.Contains(strings.ToLower(e.Error()), "duplicate") {
			json.NewEncoder(w).Encode(map[string]any{"claimed": false, "trial_days": trialDays})
			return
		}
		http.Error(w, e.Error(), 500)
		return
	}
	_, e = h.DB.ExecContext(r.Context(), `INSERT INTO subscriptions(user_external_uid,plan,status,current_period_start,current_period_end,cancel_at_period_end,created_at,updated_at) VALUES(?,?,?, ?,?,?,?,?) ON DUPLICATE KEY UPDATE plan=VALUES(plan),status=VALUES(status),current_period_start=VALUES(current_period_start),current_period_end=VALUES(current_period_end),cancel_at_period_end=TRUE,updated_at=VALUES(updated_at)`, uid, "operator", "active", now.Unix(), end.Unix(), true, now, now)
	if e != nil {
		http.Error(w, fmt.Sprintf("subscription update: %v", e), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"claimed": true, "trial_days": trialDays})
}
