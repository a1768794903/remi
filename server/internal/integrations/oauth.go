package integrations

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
)

type provider struct {
	Key           string
	AuthBase      string
	TokenEndpoint string
	Scope         string
	RedirectPath  string
}

func resolveProvider(key string) (provider, bool) {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(key, "-", "_")))
	if normalized == "gmail" || normalized == "google_mail" || normalized == "email" || normalized == "contacts" || normalized == "google_contacts" || normalized == "google_calendar" {
		return provider{Key: "google_calendar", AuthBase: "https://accounts.google.com/o/oauth2/v2/auth", TokenEndpoint: "https://oauth2.googleapis.com/token", Scope: "https://www.googleapis.com/auth/calendar", RedirectPath: "/v2/integrations/google-calendar/callback"}, true
	}
	return provider{}, false
}

func buildOAuthURL(p provider, clientID, redirectURI, state, verifier string) (string, error) {
	if p.AuthBase == "" || clientID == "" || redirectURI == "" || state == "" {
		return "", errors.New("OAuth provider configuration is incomplete")
	}
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("scope", p.Scope)
	query.Set("state", state)
	query.Set("access_type", "offline")
	query.Set("prompt", "consent")
	if verifier != "" {
		query.Set("code_challenge", pkceChallenge(verifier))
		query.Set("code_challenge_method", "S256")
	}
	return p.AuthBase + "?" + query.Encode(), nil
}

func pkceChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
