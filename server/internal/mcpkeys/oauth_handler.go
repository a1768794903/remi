package mcpkeys

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"strings"

	"remi/server/internal/auth"
)

type OAuthHandler struct {
	Service  Service
	Verifier *auth.FirebaseVerifier
}

func (h OAuthHandler) Authorize(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		if q.Get("response_type") != "code" {
			oauthError(w, "invalid_request", "response_type must be code", 400)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><h1>Omi MCP authorization</h1><p>Sign in to Omi, then submit the consent form.</p><form method="post"><input type="hidden" name="response_type" value="` + html.EscapeString(q.Get("response_type")) + `"><input type="hidden" name="client_id" value="` + html.EscapeString(q.Get("client_id")) + `"><input type="hidden" name="redirect_uri" value="` + html.EscapeString(q.Get("redirect_uri")) + `"><input type="hidden" name="resource" value="` + html.EscapeString(q.Get("resource")) + `"><input type="hidden" name="scope" value="` + html.EscapeString(q.Get("scope")) + `"><input type="hidden" name="state" value="` + html.EscapeString(q.Get("state")) + `"><input type="hidden" name="code_challenge" value="` + html.EscapeString(q.Get("code_challenge")) + `"><input type="hidden" name="code_challenge_method" value="` + html.EscapeString(q.Get("code_challenge_method")) + `"><label>Firebase ID token <input name="firebase_id_token" type="password" required></label><button type="submit">Authorize</button></form></body></html>`))
		return
	}
	if r.ParseMultipartForm(1<<20) != nil {
		_ = r.ParseForm()
	}
	_ = r.ParseForm()
	if r.FormValue("response_type") != "code" {
		oauthError(w, "invalid_request", "response_type must be code", 400)
		return
	}
	uid, err := auth.UserID(r.Context())
	if token := strings.TrimSpace(r.FormValue("firebase_id_token")); token != "" && h.Verifier != nil {
		uid, err = h.Verifier.Verify(r.Context(), token)
	}
	if err != nil {
		oauthError(w, "access_denied", "Could not verify Omi sign-in token", 401)
		return
	}
	req := AuthorizationRequest{ClientID: r.FormValue("client_id"), RedirectURI: r.FormValue("redirect_uri"), Resource: r.FormValue("resource"), Scope: r.FormValue("scope"), CodeChallenge: r.FormValue("code_challenge"), CodeChallengeMethod: r.FormValue("code_challenge_method")}
	code, err := h.Service.CreateAuthorizationCode(r.Context(), uid, req)
	if err != nil {
		oauthError(w, "invalid_request", err.Error(), 400)
		return
	}
	redirect := req.RedirectURI
	parsed, _ := url.Parse(redirect)
	params := parsed.Query()
	params.Set("code", code)
	if state := r.FormValue("state"); state != "" {
		params.Set("state", state)
	}
	parsed.RawQuery = params.Encode()
	jsonResponse(w, map[string]string{"redirect_uri": parsed.String()})
}

func (h OAuthHandler) Token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	grant := r.FormValue("grant_type")
	client := r.FormValue("client_id")
	var token TokenResponse
	var err error
	switch grant {
	case "authorization_code":
		token, err = h.Service.ExchangeCode(r.Context(), r.FormValue("code"), client, r.FormValue("client_secret"), r.FormValue("redirect_uri"), r.FormValue("code_verifier"), r.FormValue("resource"))
	case "refresh_token":
		token, err = h.Service.Refresh(r.Context(), r.FormValue("refresh_token"), client, r.FormValue("client_secret"), r.FormValue("resource"))
	default:
		oauthError(w, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token", 400)
		return
	}
	if err != nil {
		code := "invalid_grant"
		status := 400
		if err.Error() == "invalid_client" {
			code = "invalid_client"
			status = 401
		}
		if err.Error() == "invalid_target" {
			code = "invalid_target"
		}
		oauthError(w, code, err.Error(), status)
		return
	}
	jsonResponse(w, token)
}

func (h OAuthHandler) ProtectedResource(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]any{"resource": resourceURL(), "authorization_servers": []string{"/"}, "scopes_supported": []string{"memories.read", "memories.write", "conversations.read", "action_items.read", "action_items.write", "goals.read", "chat.read", "screen_activity.read", "people.read"}, "bearer_methods_supported": []string{"header"}})
}
func (h OAuthHandler) AuthorizationServer(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]any{"issuer": "/", "authorization_endpoint": "/authorize", "token_endpoint": "/token", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none", "client_secret_post"}, "scopes_supported": []string{"memories.read", "memories.write", "conversations.read", "action_items.read", "action_items.write", "goals.read", "chat.read", "screen_activity.read", "people.read"}})
}

func oauthError(w http.ResponseWriter, code, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": message})
}
func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
