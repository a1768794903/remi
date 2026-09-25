package emailprefs

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"strings"

	"database/sql"
)

const purpose = "lifecycle"

type Handler struct{ DB *sql.DB }

var invalidPage = "<html><body><h1>This unsubscribe link is invalid or has expired.</h1><p>If you followed a link from an email, please make sure you copied the whole address.</p></body></html>"
var successPage = "<html><body><h1>You have been unsubscribed.</h1><p>You will no longer receive this kind of email from Omi.</p></body></html>"

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if r.Method == http.MethodGet {
		uid, ok := verify(token)
		if !ok {
			html(w, http.StatusBadRequest, invalidPage)
			return
		}
		formToken := template.HTMLEscapeString(token)
		html(w, http.StatusOK, `<html><body><h1>Unsubscribe from Omi emails?</h1><p>You will stop receiving onboarding and re-engagement email.</p><form method="post" action="/email/unsubscribe?token=`+formToken+`"><button type="submit">Unsubscribe</button></form></body></html>`)
		_ = uid
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	uid, ok := verify(token)
	if !ok || h.DB == nil || h.optOut(r, uid) != nil {
		html(w, http.StatusBadRequest, invalidPage)
		return
	}
	html(w, http.StatusOK, successPage)
}

func (h Handler) optOut(r *http.Request, uid string) error {
	var raw []byte
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COALESCE(integrations, JSON_OBJECT()) FROM users WHERE external_uid=?`, uid).Scan(&raw); err != nil {
		// Do not reveal whether the user exists. The caller receives the same
		// neutral failure page as a bad token.
		return err
	}
	values := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &values)
	}
	values["lifecycle_email_opted_out"] = true
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	_, err = h.DB.ExecContext(r.Context(), `UPDATE users SET integrations=?, updated_at=UTC_TIMESTAMP(6) WHERE external_uid=?`, encoded, uid)
	return err
}

func verify(token string) (string, bool) {
	secret := strings.TrimSpace(os.Getenv("LIFECYCLE_EMAIL_SIGNING_SECRET"))
	if secret == "" {
		return "", false
	}
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(raw) == 0 {
		return "", false
	}
	uid := string(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(uid + ":" + purpose))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(token), []byte(base64.RawURLEncoding.EncodeToString([]byte(uid))+"."+expected)) {
		return "", false
	}
	return uid, true
}

func html(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
