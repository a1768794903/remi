package phonecalls

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"remi/server/internal/auth"
)

type Handler struct{ DB *sql.DB }

var e164 = regexp.MustCompile(`^\+[1-9]\d{1,14}$`)

func out(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func (h Handler) uid(r *http.Request) (string, error) { return auth.UserID(r.Context()) }
func twilioConfigured() bool {
	return os.Getenv("TWILIO_ACCOUNT_SID") != "" && os.Getenv("TWILIO_AUTH_TOKEN") != ""
}
func (h Handler) twilio(ctx context.Context, method, path string, form url.Values) (map[string]any, error) {
	sid := os.Getenv("TWILIO_ACCOUNT_SID")
	req, e := http.NewRequestWithContext(ctx, method, "https://api.twilio.com/2010-04-01/Accounts/"+sid+path, strings.NewReader(form.Encode()))
	if e != nil {
		return nil, e
	}
	req.SetBasicAuth(sid, os.Getenv("TWILIO_AUTH_TOKEN"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("twilio returned %s: %s", resp.Status, string(raw))
	}
	var v map[string]any
	_ = json.Unmarshal(raw, &v)
	return v, nil
}
func (h Handler) Verify(w http.ResponseWriter, r *http.Request) {
	u, e := h.uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var in struct {
		Phone string `json:"phone_number"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || !e164.MatchString(strings.TrimSpace(in.Phone)) {
		http.Error(w, "Phone number must be in E.164 format (e.g., +15551234567)", 400)
		return
	}
	in.Phone = strings.TrimSpace(in.Phone)
	var n int
	e = h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM phone_numbers WHERE user_external_uid=? AND phone_number=?`, u, in.Phone).Scan(&n)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	if n > 0 {
		http.Error(w, "Phone number already verified", 409)
		return
	}
	if !twilioConfigured() {
		http.Error(w, "Twilio is not configured", 503)
		return
	}
	v, e := h.twilio(r.Context(), http.MethodPost, "/ValidationRequests.json", url.Values{"PhoneNumber": {in.Phone}, "FriendlyName": {in.Phone}})
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	sid, _ := v["call_sid"].(string)
	code, _ := v["validation_code"].(string)
	_, e = h.DB.ExecContext(r.Context(), `INSERT INTO phone_verifications(phone_number, user_external_uid, verification_sid, created_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE user_external_uid=VALUES(user_external_uid),verification_sid=VALUES(verification_sid),created_at=VALUES(created_at)`, in.Phone, u, sid, time.Now().UTC())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	out(w, 200, map[string]any{"verification_sid": sid, "phone_number": in.Phone, "validation_code": code, "status": "pending"})
}
func (h Handler) Check(w http.ResponseWriter, r *http.Request) {
	u, e := h.uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var in struct {
		Phone string `json:"phone_number"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	in.Phone = strings.TrimSpace(in.Phone)
	var id string
	e = h.DB.QueryRowContext(r.Context(), `SELECT id FROM phone_numbers WHERE user_external_uid=? AND phone_number=?`, u, in.Phone).Scan(&id)
	if e == nil {
		out(w, 200, map[string]any{"verified": true, "phone_number_id": id})
		return
	}
	var pendingUID string
	e = h.DB.QueryRowContext(r.Context(), `SELECT user_external_uid FROM phone_verifications WHERE phone_number=?`, in.Phone).Scan(&pendingUID)
	if e != nil || pendingUID != u {
		out(w, 200, map[string]any{"verified": false})
		return
	}
	if !twilioConfigured() {
		http.Error(w, "Twilio is not configured", 503)
		return
	}
	v, e := h.twilio(r.Context(), http.MethodGet, "/OutgoingCallerIds.json?PhoneNumber="+url.QueryEscape(in.Phone), nil)
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	list, _ := v["outgoing_caller_ids"].([]any)
	if len(list) == 0 {
		out(w, 200, map[string]any{"verified": false})
		return
	}
	sid, _ := list[0].(map[string]any)["sid"].(string)
	var count int
	_ = h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM phone_numbers WHERE user_external_uid=?`, u).Scan(&count)
	id = uuid.NewString()
	_, e = h.DB.ExecContext(r.Context(), `INSERT INTO phone_numbers(id,user_external_uid,phone_number,friendly_name,twilio_sid,verified_at,is_primary,created_at) VALUES(?,?,?,?,?,?,?,?)`, id, u, in.Phone, in.Phone, sid, time.Now().UTC(), count == 0, time.Now().UTC())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	_, _ = h.DB.ExecContext(r.Context(), `DELETE FROM phone_verifications WHERE phone_number=?`, in.Phone)
	out(w, 200, map[string]any{"verified": true, "phone_number_id": id})
}
func (h Handler) Numbers(w http.ResponseWriter, r *http.Request) {
	u, e := h.uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	rows, e := h.DB.QueryContext(r.Context(), `SELECT id,phone_number,friendly_name,verified_at,is_primary FROM phone_numbers WHERE user_external_uid=? ORDER BY created_at`, u)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	defer rows.Close()
	outv := []map[string]any{}
	for rows.Next() {
		var id, num, name string
		var at time.Time
		var primary bool
		if rows.Scan(&id, &num, &name, &at, &primary) == nil {
			outv = append(outv, map[string]any{"id": id, "phone_number": num, "friendly_name": name, "verified_at": at, "is_primary": primary})
		}
	}
	out(w, 200, map[string]any{"numbers": outv})
}
func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	u, e := h.uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	id := r.PathValue("phone_number_id")
	var sid string
	e = h.DB.QueryRowContext(r.Context(), `SELECT twilio_sid FROM phone_numbers WHERE id=? AND user_external_uid=?`, id, u).Scan(&sid)
	if e == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	if sid != "" && twilioConfigured() {
		_, _ = h.twilio(r.Context(), http.MethodDelete, "/OutgoingCallerIds/"+url.PathEscape(sid)+".json", nil)
	}
	_, e = h.DB.ExecContext(r.Context(), `DELETE FROM phone_numbers WHERE id=? AND user_external_uid=?`, id, u)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	out(w, 200, map[string]bool{"success": true})
}
func (h Handler) Token(w http.ResponseWriter, r *http.Request) {
	u, e := h.uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var n int
	_ = h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM phone_numbers WHERE user_external_uid=?`, u).Scan(&n)
	if n == 0 {
		http.Error(w, "No verified phone number found. Verify a number first.", 400)
		return
	}
	sid, secret, app := os.Getenv("TWILIO_API_KEY_SID"), os.Getenv("TWILIO_API_KEY_SECRET"), os.Getenv("TWILIO_TWIML_APP_SID")
	account := os.Getenv("TWILIO_ACCOUNT_SID")
	if sid == "" || secret == "" || app == "" || account == "" {
		http.Error(w, "Twilio access token is not configured", 503)
		return
	}
	now := time.Now()
	claims := jwt.MapClaims{"jti": uuid.NewString(), "grants": map[string]any{"voice": map[string]any{"outgoing": map[string]string{"application_sid": app}, "incoming": map[string]bool{"allow": false}}}, "iss": sid, "sub": u, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
	token, e := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	out(w, 200, map[string]any{"access_token": token, "ttl": 3600, "identity": u})
}
func (h Handler) TwiML(w http.ResponseWriter, r *http.Request) {
	if !validateSignature(r) {
		http.Error(w, "Invalid Twilio signature", 403)
		return
	}
	_ = r.ParseForm()
	to := strings.TrimSpace(r.FormValue("To"))
	uid := strings.TrimPrefix(r.FormValue("From"), "client:")
	var caller string
	if uid != "" {
		_ = h.DB.QueryRowContext(r.Context(), `SELECT phone_number FROM phone_numbers WHERE user_external_uid=? AND is_primary=1 LIMIT 1`, uid).Scan(&caller)
	}
	w.Header().Set("Content-Type", "text/xml")
	if !e164.MatchString(to) || !e164.MatchString(caller) {
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response><Say>Invalid phone number or no verified caller ID.</Say></Response>`))
		return
	}
	allowed, duration, err := h.reserveQuota(r.Context(), uid, to)
	if err != nil || !allowed {
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response><Say>Monthly phone call limit reached or this destination is not available on your plan. Goodbye.</Say></Response>`))
		return
	}
	dial := `<Dial callerId="` + caller + `">`
	if duration > 0 {
		dial = `<Dial callerId="` + caller + `" timeLimit="` + strconv.Itoa(duration) + `">`
	}
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Response>` + dial + `<Number>` + to + `</Number></Dial></Response>`))
}

var countryPrefixes = []struct {
	prefix    string
	countries []string
}{
	{"+1", []string{"US", "CA"}}, {"+44", []string{"GB"}}, {"+61", []string{"AU"}}, {"+64", []string{"NZ"}},
	{"+33", []string{"FR"}}, {"+49", []string{"DE"}}, {"+34", []string{"ES"}}, {"+39", []string{"IT"}},
	{"+31", []string{"NL"}}, {"+46", []string{"SE"}}, {"+47", []string{"NO"}}, {"+45", []string{"DK"}},
	{"+358", []string{"FI"}}, {"+353", []string{"IE"}}, {"+41", []string{"CH"}}, {"+43", []string{"AT"}},
	{"+32", []string{"BE"}}, {"+351", []string{"PT"}}, {"+81", []string{"JP"}}, {"+82", []string{"KR"}},
}

func (h Handler) reserveQuota(ctx context.Context, uid, destination string) (bool, int, error) {
	var plan string
	_ = h.DB.QueryRowContext(ctx, `SELECT plan FROM subscriptions WHERE user_external_uid=?`, uid).Scan(&plan)
	if plan != "" && plan != "basic" && plan != "free" {
		return true, 0, nil
	}
	limit := 0
	if n, e := strconv.Atoi(os.Getenv("PHONE_FREE_MONTHLY_LIMIT")); e == nil && n >= 0 {
		limit = n
	}
	allowed := strings.TrimSpace(os.Getenv("PHONE_FREE_ALLOWED_COUNTRIES"))
	if allowed != "" {
		matched := false
		for _, p := range countryPrefixes {
			if strings.HasPrefix(destination, p.prefix) {
				for _, c := range p.countries {
					for _, a := range strings.Split(allowed, ",") {
						if strings.EqualFold(strings.TrimSpace(a), c) {
							matched = true
						}
					}
				}
			}
		}
		if !matched {
			return false, 0, nil
		}
	}
	month := time.Now().UTC().Format("2006-01")
	_, err := h.DB.ExecContext(ctx, `INSERT INTO phone_call_usage(user_external_uid,month_key,used,updated_at) VALUES(?,?,0,?) ON DUPLICATE KEY UPDATE updated_at=VALUES(updated_at)`, uid, month, time.Now().UTC())
	if err != nil {
		return false, 0, err
	}
	res, err := h.DB.ExecContext(ctx, `UPDATE phone_call_usage SET used=used+1,updated_at=? WHERE user_external_uid=? AND month_key=? AND used < ?`, time.Now().UTC(), uid, month, limit)
	if err != nil {
		return false, 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, 0, err
	}
	duration := 0
	if v, e := strconv.Atoi(os.Getenv("PHONE_FREE_MAX_DURATION_SECONDS")); e == nil && v > 0 {
		duration = v
	}
	return n == 1, duration, nil
}
func validateSignature(r *http.Request) bool {
	secret := os.Getenv("TWILIO_AUTH_TOKEN")
	sig := r.Header.Get("X-Twilio-Signature")
	if secret == "" || sig == "" {
		return false
	}
	_ = r.ParseForm()
	keys := make([]string, 0, len(r.PostForm))
	for k := range r.PostForm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	base := strings.TrimRight(os.Getenv("BASE_API_URL"), "/")
	raw := base + r.URL.RequestURI()
	if base == "" {
		raw = "https://" + r.Host + r.URL.RequestURI()
	}
	for _, k := range keys {
		for _, v := range r.PostForm[k] {
			raw += k + v
		}
	}
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(raw))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expected), []byte(sig)) == 1
}
