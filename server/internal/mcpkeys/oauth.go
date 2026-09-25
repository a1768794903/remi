package mcpkeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultResource = "https://api.omi.me/v1/mcp/sse"

var oauthScopes = map[string]bool{
	"memories.read": true, "memories.write": true, "conversations.read": true,
	"action_items.read": true, "action_items.write": true, "goals.read": true,
	"chat.read": true, "screen_activity.read": true, "people.read": true,
}

type AuthorizationRequest struct {
	ClientID            string
	RedirectURI         string
	Scope               string
	Resource            string
	CodeChallenge       string
	CodeChallengeMethod string
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Resource     string `json:"resource"`
}

func resourceURL() string {
	if v := strings.TrimSpace(os.Getenv("MCP_RESOURCE_URL")); v != "" {
		return v
	}
	return defaultResource
}
func normalizeOAuthScopes(raw string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, part := range strings.Fields(raw) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !oauthScopes[part] {
			return nil, fmt.Errorf("unsupported scope: %s", part)
		}
		if !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("scope is required")
	}
	return out, nil
}
func internalScopes(scopes []string) []string {
	out := make([]string, len(scopes))
	for i, v := range scopes {
		out[i] = strings.ReplaceAll(v, ".", ":")
	}
	return out
}
func outputScopes(scopes []string) string { return strings.Join(scopes, " ") }

func validateClient(clientID, clientSecret string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return errors.New("invalid client")
	}
	allowedRaw := strings.TrimSpace(os.Getenv("MCP_OAUTH_CLIENT_IDS"))
	if allowedRaw == "" {
		allowedRaw = "omi-chatgpt-prod,omi-chatgpt-dev,omi-claude-prod,omi-mcp-public"
	}
	ok := false
	for _, candidate := range strings.Split(allowedRaw, ",") {
		if strings.TrimSpace(candidate) == clientID {
			ok = true
			break
		}
	}
	if !ok {
		return errors.New("invalid client")
	}
	secrets := map[string]string{}
	if raw := strings.TrimSpace(os.Getenv("MCP_OAUTH_CLIENT_SECRETS_JSON")); raw != "" {
		_ = json.Unmarshal([]byte(raw), &secrets)
	}
	if expected, exists := secrets[clientID]; exists && subtle.ConstantTimeCompare([]byte(expected), []byte(clientSecret)) != 1 {
		return errors.New("invalid client")
	}
	return nil
}
func randomToken(prefix string) (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw := prefix + base64.RawURLEncoding.EncodeToString(b)
	return raw, hashSecret(strings.TrimPrefix(raw, prefix)), nil
}

func validateRedirect(uri string) error {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("invalid redirect_uri")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1")) {
		return errors.New("redirect_uri must use HTTPS")
	}
	allowed := strings.TrimSpace(os.Getenv("MCP_OAUTH_REDIRECT_URI_ALLOWLIST"))
	if allowed != "" {
		ok := false
		for _, item := range strings.Split(allowed, ",") {
			if strings.TrimSpace(item) == uri {
				ok = true
				break
			}
		}
		if !ok {
			return errors.New("redirect_uri is not registered")
		}
	}
	return nil
}

func (s Service) CreateAuthorizationCode(ctx context.Context, uid string, req AuthorizationRequest) (string, error) {
	if err := validateClient(req.ClientID, ""); err != nil {
		return "", err
	}
	if err := validateRedirect(req.RedirectURI); err != nil {
		return "", err
	}
	if req.CodeChallengeMethod != "S256" {
		return "", errors.New("code_challenge_method must be S256")
	}
	if len(req.CodeChallenge) < 43 || len(req.CodeChallenge) > 128 {
		return "", errors.New("invalid code_challenge")
	}
	scopes, err := normalizeOAuthScopes(req.Scope)
	if err != nil {
		return "", err
	}
	resource := req.Resource
	if resource == "" {
		resource = resourceURL()
	}
	if resource != resourceURL() {
		return "", errors.New("invalid resource")
	}
	code, codeHash, err := randomToken("mcp_code_")
	if err != nil {
		return "", err
	}
	idRaw, _, err := randomToken("")
	if err != nil {
		return "", err
	}
	id := strings.TrimPrefix(idRaw, "mcp_")
	now := time.Now().UTC()
	scopeJSON, _ := json.Marshal(scopes)
	_, err = s.DB.ExecContext(ctx, `INSERT INTO mcp_oauth_authorization_codes (id,code_hash,user_external_uid,client_id,redirect_uri,resource,scopes,code_challenge,code_challenge_method,created_at,expires_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, id, codeHash, uid, req.ClientID, req.RedirectURI, resource, scopeJSON, req.CodeChallenge, req.CodeChallengeMethod, now, now.Add(10*time.Minute))
	return code, err
}

func verifyPKCE(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

func (s Service) issueTokens(ctx context.Context, grantID, uid, clientID, resource string, scopes []string) (TokenResponse, error) {
	access, accessHash, err := randomToken("mcp_at_")
	if err != nil {
		return TokenResponse{}, err
	}
	refresh, refreshHash, err := randomToken("mcp_rt_")
	if err != nil {
		return TokenResponse{}, err
	}
	now := time.Now().UTC()
	accessID := strings.TrimPrefix(access, "mcp_at_")
	refreshID := strings.TrimPrefix(refresh, "mcp_rt_")
	scopeJSON, _ := json.Marshal(scopes)
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO mcp_oauth_access_tokens (id,token_hash,grant_id,user_external_uid,client_id,resource,scopes,created_at,expires_at) VALUES (?,?,?,?,?,?,?,?,?)`, accessID, accessHash, grantID, uid, clientID, resource, scopeJSON, now, now.Add(time.Hour)); err != nil {
		return TokenResponse{}, err
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO mcp_oauth_refresh_tokens (id,token_hash,grant_id,user_external_uid,client_id,resource,scopes,created_at,expires_at) VALUES (?,?,?,?,?,?,?,?,?)`, refreshID, refreshHash, grantID, uid, clientID, resource, scopeJSON, now, now.Add(365*24*time.Hour)); err != nil {
		return TokenResponse{}, err
	}
	return TokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: 3600, RefreshToken: refresh, Scope: outputScopes(scopes), Resource: resource}, nil
}

func (s Service) ExchangeCode(ctx context.Context, code, clientID, clientSecret, redirectURI, verifier, requestedResource string) (TokenResponse, error) {
	if err := validateClient(clientID, clientSecret); err != nil {
		return TokenResponse{}, errors.New("invalid_client")
	}
	codeHash := hashSecret(strings.TrimPrefix(code, "mcp_code_"))
	var id, uid, storedClient, storedRedirect, resource, challenge, method string
	var scopeJSON []byte
	var expires time.Time
	err := s.DB.QueryRowContext(ctx, `SELECT id,user_external_uid,client_id,redirect_uri,resource,scopes,code_challenge,code_challenge_method,expires_at FROM mcp_oauth_authorization_codes WHERE code_hash=? AND used_at IS NULL`, codeHash).Scan(&id, &uid, &storedClient, &storedRedirect, &resource, &scopeJSON, &challenge, &method, &expires)
	if err != nil {
		return TokenResponse{}, errors.New("invalid_grant")
	}
	if time.Now().UTC().After(expires) || storedClient != clientID || storedRedirect != redirectURI || method != "S256" || !verifyPKCE(verifier, challenge) {
		return TokenResponse{}, errors.New("invalid_grant")
	}
	if requestedResource != "" && requestedResource != resource {
		return TokenResponse{}, errors.New("invalid_target")
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE mcp_oauth_authorization_codes SET used_at=NOW(6) WHERE id=? AND used_at IS NULL`, id)
	if err != nil {
		return TokenResponse{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return TokenResponse{}, errors.New("invalid_grant")
	}
	var scopes []string
	if json.Unmarshal(scopeJSON, &scopes) != nil {
		return TokenResponse{}, errors.New("invalid_grant")
	}
	grantID := id
	_, err = s.DB.ExecContext(ctx, `INSERT INTO mcp_oauth_grants (id,user_external_uid,client_id,client_name,resource,scopes,created_at) VALUES (?,?,?,?,?,?,NOW(6))`, grantID, uid, clientID, clientID, resource, scopeJSON)
	if err != nil {
		return TokenResponse{}, err
	}
	return s.issueTokens(ctx, grantID, uid, clientID, resource, scopes)
}

func (s Service) Refresh(ctx context.Context, refresh, clientID, clientSecret, requestedResource string) (TokenResponse, error) {
	if err := validateClient(clientID, clientSecret); err != nil {
		return TokenResponse{}, errors.New("invalid_client")
	}
	hash := hashSecret(strings.TrimPrefix(refresh, "mcp_rt_"))
	var id, grantID, uid, storedClient, resource string
	var scopeJSON []byte
	var expires time.Time
	var used, revoked sqlNullTime
	err := s.DB.QueryRowContext(ctx, `SELECT r.id,r.grant_id,r.user_external_uid,r.client_id,r.resource,r.scopes,r.expires_at,r.used_at,r.revoked_at FROM mcp_oauth_refresh_tokens r JOIN mcp_oauth_grants g ON g.id=r.grant_id WHERE r.token_hash=? AND g.revoked_at IS NULL`, hash).Scan(&id, &grantID, &uid, &storedClient, &resource, &scopeJSON, &expires, &used, &revoked)
	if err != nil || storedClient != clientID || time.Now().UTC().After(expires) || revoked.Valid {
		return TokenResponse{}, errors.New("invalid_grant")
	}
	if requestedResource != "" && requestedResource != resource {
		return TokenResponse{}, errors.New("invalid_target")
	}
	if used.Valid {
		_, _ = s.DB.ExecContext(ctx, `UPDATE mcp_oauth_grants SET revoked_at=NOW(6) WHERE id=?`, grantID)
		_, _ = s.DB.ExecContext(ctx, `UPDATE mcp_oauth_access_tokens SET revoked_at=NOW(6) WHERE grant_id=?`, grantID)
		_, _ = s.DB.ExecContext(ctx, `UPDATE mcp_oauth_refresh_tokens SET revoked_at=NOW(6) WHERE grant_id=?`, grantID)
		return TokenResponse{}, errors.New("invalid_grant")
	}
	var scopes []string
	if json.Unmarshal(scopeJSON, &scopes) != nil {
		return TokenResponse{}, errors.New("invalid_grant")
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE mcp_oauth_refresh_tokens SET used_at=NOW(6) WHERE id=? AND used_at IS NULL`, id)
	if err != nil {
		return TokenResponse{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return TokenResponse{}, errors.New("invalid_grant")
	}
	tokens, err := s.issueTokens(ctx, grantID, uid, clientID, resource, scopes)
	if err != nil {
		return TokenResponse{}, err
	}
	newID := strings.TrimPrefix(tokens.RefreshToken, "mcp_rt_")
	_, _ = s.DB.ExecContext(ctx, `UPDATE mcp_oauth_refresh_tokens SET replaced_by=? WHERE id=?`, newID, id)
	return tokens, nil
}

type sqlNullTime struct {
	Time  time.Time
	Valid bool
}

func (n *sqlNullTime) Scan(value any) error {
	if value == nil {
		n.Valid = false
		return nil
	}
	switch v := value.(type) {
	case time.Time:
		n.Time = v
		n.Valid = true
		return nil
	case []byte:
		t, e := time.Parse("2006-01-02 15:04:05.999999", string(v))
		n.Time = t
		n.Valid = e == nil
		return e
	}
	return errors.New("invalid nullable time")
}

func (s Service) AuthenticateOAuth(ctx context.Context, token string) (AuthContext, error) {
	if !strings.HasPrefix(token, "mcp_at_") {
		return AuthContext{}, ErrNotFound
	}
	var uid string
	var scopesJSON []byte
	var expires time.Time
	err := s.DB.QueryRowContext(ctx, `SELECT a.user_external_uid,a.scopes,a.expires_at FROM mcp_oauth_access_tokens a JOIN mcp_oauth_grants g ON g.id=a.grant_id WHERE a.token_hash=? AND a.revoked_at IS NULL AND g.revoked_at IS NULL`, hashSecret(strings.TrimPrefix(token, "mcp_at_"))).Scan(&uid, &scopesJSON, &expires)
	if err != nil || time.Now().UTC().After(expires) {
		return AuthContext{}, ErrNotFound
	}
	var scopes []string
	if json.Unmarshal(scopesJSON, &scopes) != nil {
		return AuthContext{}, ErrNotFound
	}
	return AuthContext{UID: uid, Scopes: internalScopes(scopes)}, nil
}
