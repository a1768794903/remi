package mcpkeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"remi/server/internal/auth"
)

var ErrNotFound = errors.New("mcp api key not found")

type Key struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	AppID      *string    `json:"app_id,omitempty"`
	Scopes     []string   `json:"scopes,omitempty"`
}

type Service struct{ DB *sql.DB }

type AuthContext struct {
	UID    string
	Scopes []string
}

type Grant struct {
	ID         string     `json:"id"`
	ClientID   string     `json:"client_id"`
	ClientName string     `json:"client_name"`
	Resource   string     `json:"resource"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func generateSecret() (raw, prefix, hash string, err error) {
	b := make([]byte, 16)
	if _, err = rand.Read(b); err != nil {
		return
	}
	secret := hex.EncodeToString(b)
	raw = "omi_mcp_" + secret
	prefix = "omi_mcp_" + secret[:4] + "..." + secret[len(secret)-4:]
	hash = hashSecret(secret)
	return
}

func (s Service) Create(ctx context.Context, uid, name string) (Key, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Key{}, "", errors.New("key name cannot be empty")
	}
	if strings.Contains(name, "omi_mcp_") {
		return Key{}, "", errors.New("key name must not contain a raw API key")
	}
	raw, prefix, hash, err := generateSecret()
	if err != nil {
		return Key{}, "", err
	}
	idBytes := make([]byte, 16)
	if _, err = rand.Read(idBytes); err != nil {
		return Key{}, "", err
	}
	id := hex.EncodeToString(idBytes)
	now := time.Now().UTC()
	fullScopes := []string{"memories:read", "memories:write", "conversations:read", "action_items:read", "action_items:write", "goals:read", "chat:read", "screen_activity:read", "people:read"}
	scopeJSON, _ := json.Marshal(fullScopes)
	_, err = s.DB.ExecContext(ctx, `INSERT INTO mcp_api_keys (id,user_external_uid,name,key_prefix,hashed_key,app_id,scopes,created_at) VALUES (?,?,?,?,?,?,?,?)`, id, uid, name, prefix, hash, "omi_mcp", scopeJSON, now)
	if err != nil {
		return Key{}, "", err
	}
	return Key{ID: id, Name: name, KeyPrefix: prefix, CreatedAt: now, AppID: stringPtr("omi_mcp"), Scopes: fullScopes}, raw, nil
}

func (s Service) List(ctx context.Context, uid string) ([]Key, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,key_prefix,created_at,last_used_at,app_id,scopes FROM mcp_api_keys WHERE user_external_uid=? ORDER BY created_at DESC,id DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		var app, scopes sql.NullString
		var last sql.NullTime
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.CreatedAt, &last, &app, &scopes); err != nil {
			return nil, err
		}
		if last.Valid {
			k.LastUsedAt = &last.Time
		}
		if app.Valid {
			k.AppID = &app.String
		}
		if scopes.Valid {
			_ = json.Unmarshal([]byte(scopes.String), &k.Scopes)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s Service) Delete(ctx context.Context, uid, id string) error {
	result, err := s.DB.ExecContext(ctx, `DELETE FROM mcp_api_keys WHERE id=? AND user_external_uid=?`, id, uid)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s Service) Grants(ctx context.Context, uid string) ([]Grant, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,client_id,client_name,resource,scopes,created_at,last_used_at FROM mcp_oauth_grants WHERE user_external_uid=? AND revoked_at IS NULL ORDER BY created_at DESC,id DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Grant{}
	for rows.Next() {
		var g Grant
		var scopes sql.NullString
		var last sql.NullTime
		if err := rows.Scan(&g.ID, &g.ClientID, &g.ClientName, &g.Resource, &scopes, &g.CreatedAt, &last); err != nil {
			return nil, err
		}
		if scopes.Valid {
			_ = json.Unmarshal([]byte(scopes.String), &g.Scopes)
		}
		if last.Valid {
			g.LastUsedAt = &last.Time
		}
		result = append(result, g)
	}
	return result, rows.Err()
}

func (s Service) RevokeGrant(ctx context.Context, uid, id string) error {
	result, err := s.DB.ExecContext(ctx, `UPDATE mcp_oauth_grants SET revoked_at=NOW(6) WHERE id=? AND user_external_uid=? AND revoked_at IS NULL`, id, uid)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE mcp_oauth_access_tokens SET revoked_at=NOW(6) WHERE grant_id=?`, id)
	_, _ = s.DB.ExecContext(ctx, `UPDATE mcp_oauth_refresh_tokens SET revoked_at=NOW(6) WHERE grant_id=?`, id)
	return nil
}

func (s Service) Authenticate(ctx context.Context, token string) (string, error) {
	context, err := s.AuthenticateContext(ctx, token)
	if err != nil {
		return "", err
	}
	return context.UID, nil
}

func (s Service) AuthenticateContext(ctx context.Context, token string) (AuthContext, error) {
	if strings.HasPrefix(token, "mcp_at_") {
		return s.AuthenticateOAuth(ctx, token)
	}
	if !strings.HasPrefix(token, "omi_mcp_") {
		return AuthContext{}, ErrNotFound
	}
	secret := strings.TrimPrefix(token, "omi_mcp_")
	if len(secret) != 32 {
		return AuthContext{}, ErrNotFound
	}
	var uid, id string
	var scopes sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT user_external_uid,id,scopes FROM mcp_api_keys WHERE hashed_key=?`, hashSecret(secret)).Scan(&uid, &id, &scopes)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthContext{}, ErrNotFound
	}
	if err != nil {
		return AuthContext{}, err
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE mcp_api_keys SET last_used_at=NOW(6) WHERE id=?`, id)
	result := AuthContext{UID: uid, Scopes: []string{}}
	if scopes.Valid {
		_ = json.Unmarshal([]byte(scopes.String), &result.Scopes)
	}
	return result, nil
}

func stringPtr(v string) *string { return &v }

type Handler struct{ Service Service }

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	items, err := h.Service.List(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, items)
}
func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	item, raw, err := h.Service.Create(r.Context(), uid, in.Name)
	if err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	payload := map[string]any{"id": item.ID, "name": item.Name, "key_prefix": item.KeyPrefix, "created_at": item.CreatedAt, "app_id": item.AppID, "scopes": item.Scopes, "key": raw}
	writeJSON(w, payload)
}
func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	err = h.Service.Delete(r.Context(), uid, r.PathValue("key_id"))
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h Handler) Grants(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	items, err := h.Service.Grants(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"grants": items})
}
func (h Handler) RevokeGrant(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	err = h.Service.RevokeGrant(r.Context(), uid, r.PathValue("grant_id"))
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
