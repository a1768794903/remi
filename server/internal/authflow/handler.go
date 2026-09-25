package authflow

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const sessionTTL = 5 * time.Minute
const codeTTL = 5 * time.Minute

type Handler struct {
	Redis   *redis.Client
	Client  *http.Client
	BaseURL string
}
type session struct {
	Provider, RedirectURI, State, CodeChallenge, CodeChallengeMethod, ReferralCode string
	CreatedAt                                                                      int64
}
type credentials struct{ Provider, IDToken, AccessToken, ProviderID, FullName string }
type authCode struct {
	Credentials                                                             credentials
	RedirectURI, CodeChallenge, CodeChallengeMethod, Provider, ReferralCode string
	CreatedAt                                                               int64
}

func (h Handler) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}
func (h Handler) baseURL() string {
	if h.BaseURL != "" {
		return strings.TrimRight(h.BaseURL, "/")
	}
	return strings.TrimRight(os.Getenv("BASE_API_URL"), "/")
}
func sessionKey(id string) string { return "remi:auth-session:" + id }
func codeKey(id string) string    { return "remi:auth-code:" + id }

func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || strings.ContainsAny(raw, "\r\n") {
		return false
	}
	if u.Scheme == "omi" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

var pkceChars = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func validPKCE(challenge, method string) bool {
	return pkceChars.MatchString(challenge) && strings.EqualFold(method, "S256")
}
func pkce(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func formError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": detail})
}

func (h Handler) Authorize(w http.ResponseWriter, r *http.Request) {
	if h.Redis == nil {
		formError(w, 503, "authentication storage unavailable")
		return
	}
	provider := r.URL.Query().Get("provider")
	if provider != "google" && provider != "apple" {
		formError(w, 400, "Unsupported provider")
		return
	}
	redirect := r.URL.Query().Get("redirect_uri")
	if !validRedirect(redirect) {
		formError(w, 400, "invalid redirect_uri")
		return
	}
	challenge, method := r.URL.Query().Get("code_challenge"), r.URL.Query().Get("code_challenge_method")
	if !validPKCE(challenge, method) {
		formError(w, 400, "code_challenge_method must be S256")
		return
	}
	referral := ""
	if c, err := r.Cookie("referral_code"); err == nil {
		referral = c.Value
	}
	id := uuid.NewString()
	data, _ := json.Marshal(session{Provider: provider, RedirectURI: redirect, State: r.URL.Query().Get("state"), CodeChallenge: challenge, CodeChallengeMethod: "S256", ReferralCode: referral, CreatedAt: time.Now().Unix()})
	if err := h.Redis.Set(r.Context(), sessionKey(id), data, sessionTTL).Err(); err != nil {
		formError(w, 503, "authentication storage unavailable")
		return
	}
	base := h.baseURL()
	if base == "" {
		formError(w, 500, "BASE_API_URL not configured")
		return
	}
	callback := base + "/v1/auth/callback/" + provider
	var endpoint string
	if provider == "google" {
		clientID := os.Getenv("GOOGLE_CLIENT_ID")
		if clientID == "" {
			formError(w, 500, "Google client ID not configured")
			return
		}
		endpoint = "https://accounts.google.com/o/oauth2/v2/auth?" + url.Values{"client_id": {clientID}, "redirect_uri": {callback}, "response_type": {"code"}, "scope": {"openid email profile"}, "state": {id}}.Encode()
	} else {
		clientID := os.Getenv("APPLE_CLIENT_ID")
		if clientID == "" {
			formError(w, 500, "Apple client ID not configured")
			return
		}
		endpoint = "https://appleid.apple.com/auth/authorize?" + url.Values{"client_id": {clientID}, "redirect_uri": {callback}, "response_type": {"code"}, "scope": {"name email"}, "response_mode": {"form_post"}, "state": {id}}.Encode()
	}
	http.Redirect(w, r, endpoint, http.StatusFound)
}

func (h Handler) callback(w http.ResponseWriter, r *http.Request, provider, code, state, providerError, fullName string) {
	if providerError != "" {
		formError(w, 400, "Auth error: "+providerError)
		return
	}
	if code == "" || state == "" || h.Redis == nil {
		formError(w, 400, "Invalid auth session")
		return
	}
	raw, err := h.Redis.GetDel(r.Context(), sessionKey(state)).Bytes()
	if err != nil {
		formError(w, 400, "Invalid auth session")
		return
	}
	var s session
	if json.Unmarshal(raw, &s) != nil || s.Provider != provider {
		formError(w, 400, "Invalid auth session")
		return
	}
	cred, err := h.exchange(r.Context(), provider, code)
	if err != nil {
		formError(w, 400, err.Error())
		return
	}
	cred.FullName = fullName
	stored, _ := json.Marshal(authCode{Credentials: cred, RedirectURI: s.RedirectURI, CodeChallenge: s.CodeChallenge, CodeChallengeMethod: s.CodeChallengeMethod, Provider: provider, ReferralCode: s.ReferralCode, CreatedAt: time.Now().Unix()})
	temporary := uuid.NewString()
	if err := h.Redis.Set(r.Context(), codeKey(temporary), stored, codeTTL).Err(); err != nil {
		formError(w, 503, "authentication storage unavailable")
		return
	}
	callbackURL := s.RedirectURI + "?code=" + url.QueryEscape(temporary)
	if s.State != "" {
		callbackURL += "&state=" + url.QueryEscape(s.State)
	}
	quoted, _ := json.Marshal(callbackURL)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, "<!doctype html><html><body><script>location.replace("+string(template.JS(string(quoted)))+")</script><a href=\""+template.HTMLEscapeString(callbackURL)+"\">Continue</a></body></html>")
}
func (h Handler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	h.callback(w, r, "google", r.URL.Query().Get("code"), r.URL.Query().Get("state"), r.URL.Query().Get("error"), "")
}
func (h Handler) AppleCallback(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	fullName := ""
	if raw := r.FormValue("user"); raw != "" {
		var v struct {
			Name struct {
				FirstName string `json:"firstName"`
				LastName  string `json:"lastName"`
			} `json:"name"`
		}
		if json.Unmarshal([]byte(raw), &v) == nil {
			fullName = strings.TrimSpace(v.Name.FirstName + " " + v.Name.LastName)
		}
	}
	h.callback(w, r, "apple", r.FormValue("code"), r.FormValue("state"), r.FormValue("error"), fullName)
}

func (h Handler) exchange(ctx context.Context, provider, code string) (credentials, error) {
	base := h.baseURL()
	if base == "" {
		return credentials{}, errors.New("BASE_API_URL not configured")
	}
	values := url.Values{"code": {code}, "redirect_uri": {base + "/v1/auth/callback/" + provider}, "grant_type": {"authorization_code"}}
	var endpoint string
	if provider == "google" {
		values.Set("client_id", os.Getenv("GOOGLE_CLIENT_ID"))
		values.Set("client_secret", os.Getenv("GOOGLE_CLIENT_SECRET"))
		endpoint = "https://oauth2.googleapis.com/token"
	} else {
		values.Set("client_id", os.Getenv("APPLE_CLIENT_ID"))
		secret, err := appleSecret()
		if err != nil {
			return credentials{}, err
		}
		values.Set("client_secret", secret)
		endpoint = "https://appleid.apple.com/auth/token"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return credentials{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.client().Do(req)
	if err != nil {
		return credentials{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return credentials{}, fmt.Errorf("failed to exchange %s code", provider)
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return credentials{}, errors.New("invalid provider token response")
	}
	id, _ := raw["id_token"].(string)
	access, _ := raw["access_token"].(string)
	if id == "" || (provider == "google" && access == "") {
		return credentials{}, errors.New("invalid provider token response")
	}
	return credentials{Provider: provider, IDToken: id, AccessToken: access, ProviderID: provider + ".com"}, nil
}

func appleSecret() (string, error) {
	clientID, teamID, keyID, rawKey := os.Getenv("APPLE_CLIENT_ID"), os.Getenv("APPLE_TEAM_ID"), os.Getenv("APPLE_KEY_ID"), os.Getenv("APPLE_PRIVATE_KEY")
	if clientID == "" || teamID == "" || keyID == "" || rawKey == "" {
		return "", errors.New("Apple authentication not properly configured")
	}
	block, _ := pem.Decode([]byte(strings.ReplaceAll(rawKey, `\\n`, "\n")))
	if block == nil {
		return "", errors.New("invalid Apple private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", err
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"iss": teamID, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(), "aud": "https://appleid.apple.com", "sub": clientID})
	token.Header["kid"] = keyID
	return token.SignedString(key)
}

func (h Handler) Token(w http.ResponseWriter, r *http.Request) {
	if h.Redis == nil {
		formError(w, 503, "authentication storage unavailable")
		return
	}
	_ = r.ParseForm()
	if r.FormValue("grant_type") != "authorization_code" {
		formError(w, 400, "Unsupported grant type")
		return
	}
	raw, err := h.Redis.GetDel(r.Context(), codeKey(r.FormValue("code"))).Bytes()
	if err != nil {
		formError(w, 400, "Invalid or expired code")
		return
	}
	var stored authCode
	if json.Unmarshal(raw, &stored) != nil || stored.RedirectURI == "" || stored.RedirectURI != r.FormValue("redirect_uri") {
		formError(w, 400, "redirect_uri mismatch")
		return
	}
	if !validPKCE(stored.CodeChallenge, stored.CodeChallengeMethod) || pkce(r.FormValue("code_verifier")) != stored.CodeChallenge {
		formError(w, 400, "invalid code_verifier")
		return
	}
	uid, firebaseIDToken, err := h.firebaseSignIn(r.Context(), stored.Credentials)
	if err != nil {
		formError(w, 502, "Failed to generate authentication token")
		return
	}
	result := map[string]any{"provider": stored.Credentials.Provider, "id_token": stored.Credentials.IDToken, "access_token": stored.Credentials.AccessToken, "provider_id": stored.Credentials.ProviderID, "token_type": "Bearer", "expires_in": 3600}
	if strings.EqualFold(r.FormValue("use_custom_token"), "true") || r.FormValue("use_custom_token") == "1" {
		custom, e := firebaseCustomToken(uid)
		if e != nil {
			formError(w, 502, "Failed to generate authentication token")
			return
		}
		result["custom_token"] = custom
	}
	if firebaseIDToken != "" {
		result["firebase_id_token"] = firebaseIDToken
	}
	_ = json.NewEncoder(w).Encode(result)
}

func (h Handler) LocalDevCustomToken(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(os.Getenv("FIREBASE_AUTH_EMULATOR_HOST")) == "" {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	uid := strings.TrimSpace(r.FormValue("uid"))
	if uid == "" {
		uid = "local-dev-user"
	}
	token, err := firebaseCustomToken(uid)
	if err != nil {
		formError(w, 500, "Could not create local development user")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"custom_token": token, "uid": uid, "provider": "local_dev"})
}
func (h Handler) firebaseSignIn(ctx context.Context, c credentials) (string, string, error) {
	key := os.Getenv("FIREBASE_API_KEY")
	if key == "" {
		return "", "", errors.New("FIREBASE_API_KEY not configured")
	}
	body, _ := json.Marshal(map[string]any{"postBody": "id_token=" + url.QueryEscape(c.IDToken) + "&providerId=" + url.QueryEscape(c.ProviderID) + "&access_token=" + url.QueryEscape(c.AccessToken), "requestUri": "http://localhost", "returnIdpCredential": true, "returnSecureToken": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://identitytoolkit.googleapis.com/v1/accounts:signInWithIdp?key="+url.QueryEscape(key), strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", errors.New("Firebase sign-in failed")
	}
	var out struct {
		LocalID, IDToken string `json:"localId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.LocalID == "" {
		return "", "", errors.New("Firebase sign-in returned no UID")
	}
	return out.LocalID, out.IDToken, nil
}

func serviceAccount() (map[string]string, error) {
	data := []byte(os.Getenv("SERVICE_ACCOUNT_JSON"))
	if len(data) == 0 {
		path := os.Getenv("FIREBASE_AUTH_CREDENTIALS_PATH")
		if path == "" {
			path = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
		}
		if path != "" {
			var err error
			data, err = os.ReadFile(filepath.Clean(path))
			if err != nil {
				return nil, err
			}
		}
	}
	if len(data) == 0 {
		return nil, errors.New("Firebase service account not configured")
	}
	var v map[string]string
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}
func firebaseCustomToken(uid string) (string, error) {
	sa, err := serviceAccount()
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode([]byte(strings.ReplaceAll(sa["private_key"], `\\n`, "\n")))
	if block == nil {
		return "", errors.New("invalid Firebase service account key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", err
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return "", errors.New("Firebase service account key is not RSA")
	}
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": sa["client_email"], "sub": sa["client_email"], "aud": "https://identitytoolkit.googleapis.com/google.identity.identitytoolkit.v1.IdentityToolkit", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "uid": uid})
	return token.SignedString(rsaKey)
}
