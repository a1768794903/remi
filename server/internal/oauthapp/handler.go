package oauthapp

import (
	"crypto/hmac"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"strings"
	"time"

	"remi/server/internal/auth"
)

const csrfCookie = "omi_oauth_csrf"

type Handler struct {
	DB       *sql.DB
	Verifier *auth.FirebaseVerifier
}

func randomToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func app(h Handler, r *http.Request) (id, name, home string, private, paid bool, owner string, caps []string, ok bool) {
	id = strings.TrimSpace(r.FormValue("app_id"))
	if id == "" {
		return
	}
	var raw, capRaw []byte
	var priv, ipaid bool
	if e := h.DB.QueryRowContext(r.Context(), `SELECT name,uid,private,is_paid,external_integration,capabilities FROM plugins_data WHERE id=? AND approved=1`, id).Scan(&name, &owner, &priv, &ipaid, &raw, &capRaw); e != nil {
		return
	}
	var ext map[string]any
	_ = json.Unmarshal(raw, &ext)
	home, _ = ext["app_home_url"].(string)
	if home == "" {
		return
	}
	_ = json.Unmarshal(capRaw, &caps)
	return id, name, home, priv, ipaid, owner, caps, true
}
func (h Handler) Authorize(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		http.Error(w, "OAuth storage is not configured", 503)
		return
	}
	id, name, home, _, _, _, caps, ok := app(h, r)
	if !ok {
		http.Error(w, "App not found or not configured for OAuth", 404)
		return
	}
	token := randomToken()
	if token == "" {
		http.Error(w, "could not create authorization state", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: token, MaxAge: 600, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, Path: "/"})
	state := html.EscapeString(r.URL.Query().Get("state"))
	perms := []string{"Access your basic Omi profile information."}
	if len(caps) > 0 {
		perms = perms[:0]
		for _, c := range caps {
			perms = append(perms, "Access the "+html.EscapeString(c)+" capability.")
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Authorize ` + html.EscapeString(name) + `</title></head><body><h1>Connect ` + html.EscapeString(name) + `</h1><p>Review the requested permissions and sign in to continue.</p><ul>` + func() string {
		var b strings.Builder
		for _, p := range perms {
			b.WriteString("<li>" + p + "</li>")
		}
		return b.String()
	}() + `</ul><form method="post" action="/v1/oauth/token"><input type="hidden" name="app_id" value="` + html.EscapeString(id) + `"><input type="hidden" name="state" value="` + state + `"><input type="hidden" name="csrf_token" value="` + html.EscapeString(token) + `"><input name="firebase_id_token" type="password" required><button type="submit">Authorize</button></form><p>Redirect: ` + html.EscapeString(home) + `</p></body></html>`))
}
func (h Handler) Token(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		http.Error(w, "OAuth storage is not configured", 503)
		return
	}
	if r.ParseForm() != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	csrf := r.FormValue("csrf_token")
	cookie, e := r.Cookie(csrfCookie)
	if e != nil || csrf == "" || cookie.Value == "" || !hmac.Equal([]byte(csrf), []byte(cookie.Value)) {
		http.Error(w, "This authorization request is invalid or expired", 403)
		return
	}
	uid, e := h.Verifier.Verify(r.Context(), r.FormValue("firebase_id_token"))
	if e != nil {
		http.Error(w, "invalid Firebase ID token", 401)
		return
	}
	id, name, home, private, paid, owner, _, ok := app(h, r)
	_ = name
	if !ok {
		http.Error(w, "App not found", 404)
		return
	}
	if private && owner != uid {
		http.Error(w, "This app is private and you are not authorized", 403)
		return
	}
	if paid {
		var n int
		if h.DB.QueryRowContext(r.Context(), `SELECT 1 FROM user_enabled_apps WHERE user_external_uid=? AND app_id=?`, uid, id).Scan(&n) != nil {
			http.Error(w, "paid app must be purchased before authorization", 403)
			return
		}
	}
	if _, e = h.DB.ExecContext(r.Context(), `INSERT IGNORE INTO user_enabled_apps(user_external_uid,app_id,created_at) VALUES(?,?,?)`, uid, id, time.Now().UTC()); e != nil {
		http.Error(w, "could not enable app", 503)
		return
	}
	writeJSON(w, map[string]any{"uid": uid, "redirect_url": home, "state": r.FormValue("state")})
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
