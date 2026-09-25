package devkeys

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

var ErrNotFound = errors.New("developer API key not found")
var AvailableScopes = []string{"conversations:read", "conversations:write", "memories:read", "memories:write", "action_items:read", "action_items:write", "goals:read", "goals:write"}
var ReadOnlyScopes = []string{"conversations:read", "memories:read", "action_items:read", "goals:read"}

var ErrInvalidKey = errors.New("invalid developer API key")
var ErrInsufficientScope = errors.New("developer API key does not have the required scope")

type Key struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Scopes     []string   `json:"scopes,omitempty"`
}
type Service struct{ DB *sql.DB }

func hash(v string) string { s := sha256.Sum256([]byte(v)); return hex.EncodeToString(s[:]) }
func validScopes(scopes []string) bool {
	for _, wanted := range scopes {
		found := false
		for _, allowed := range AvailableScopes {
			if wanted == allowed {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func cloneScopes(scopes []string) []string { return append([]string(nil), scopes...) }
func (s Service) Create(ctx context.Context, uid, name string, scopes []string) (Key, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Key{}, "", errors.New("key name cannot be empty")
	}
	if strings.Contains(name, "omi_dev_") {
		return Key{}, "", errors.New("key name must not contain a raw API key")
	}
	if scopes == nil {
		scopes = cloneScopes(ReadOnlyScopes)
	}
	if !validScopes(scopes) {
		return Key{}, "", errors.New("invalid developer API key scope")
	}
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return Key{}, "", e
	}
	secret := hex.EncodeToString(b)
	raw := "omi_dev_" + secret
	prefix := "omi_dev_" + secret[:4] + "..." + secret[len(secret)-4:]
	idb := make([]byte, 16)
	if _, e := rand.Read(idb); e != nil {
		return Key{}, "", e
	}
	id := hex.EncodeToString(idb)
	sc, _ := json.Marshal(scopes)
	now := time.Now().UTC()
	_, e := s.DB.ExecContext(ctx, `INSERT INTO dev_api_keys (id,user_external_uid,name,key_prefix,hashed_key,scopes,created_at) VALUES (?,?,?,?,?,?,?)`, id, uid, name, prefix, hash(secret), sc, now)
	if e != nil {
		return Key{}, "", e
	}
	return Key{ID: id, Name: name, KeyPrefix: prefix, CreatedAt: now, Scopes: scopes}, raw, nil
}
func (s Service) List(ctx context.Context, uid string) ([]Key, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT id,name,key_prefix,created_at,last_used_at,scopes FROM dev_api_keys WHERE user_external_uid=? ORDER BY created_at DESC,id DESC`, uid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		var last sql.NullTime
		var sc sql.NullString
		if e = rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.CreatedAt, &last, &sc); e != nil {
			return nil, e
		}
		if last.Valid {
			k.LastUsedAt = &last.Time
		}
		if sc.Valid {
			_ = json.Unmarshal([]byte(sc.String), &k.Scopes)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (s Service) Delete(ctx context.Context, uid, id string) error {
	r, e := s.DB.ExecContext(ctx, `DELETE FROM dev_api_keys WHERE id=? AND user_external_uid=?`, id, uid)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Authenticate validates a raw developer key and records its last use. The
// key secret is never stored in plaintext; only its SHA-256 digest is looked
// up. Developer keys are deliberately limited to the integration API surface
// here; regular Firebase authentication remains the path for user endpoints.
func (s Service) Authenticate(ctx context.Context, raw, method, path string) (string, error) {
	if !strings.HasPrefix(raw, "omi_dev_") || len(raw) != len("omi_dev_")+32 {
		return "", ErrInvalidKey
	}
	secret := strings.TrimPrefix(raw, "omi_dev_")
	var uid, encoded string
	var id string
	if err := s.DB.QueryRowContext(ctx, `SELECT id,user_external_uid,scopes FROM dev_api_keys WHERE hashed_key=?`, hash(secret)).Scan(&id, &uid, &encoded); err != nil {
		return "", ErrInvalidKey
	}
	var scopes []string
	if err := json.Unmarshal([]byte(encoded), &scopes); err != nil {
		return "", ErrInvalidKey
	}
	required := requiredScope(method, path)
	if required != "" && !containsScope(scopes, required) {
		return "", ErrInsufficientScope
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE dev_api_keys SET last_used_at=? WHERE id=?`, time.Now().UTC(), id)
	return uid, nil
}

func containsScope(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func requiredScope(method, path string) string {
	if strings.HasPrefix(path, "/v1/dev/user/memories") {
		if method == http.MethodGet {
			return "memories:read"
		}
		return "memories:write"
	}
	if strings.HasPrefix(path, "/v1/dev/user/action-items") {
		if method == http.MethodGet {
			return "action_items:read"
		}
		return "action_items:write"
	}
	if strings.HasPrefix(path, "/v1/dev/user/goals") {
		if method == http.MethodGet {
			return "goals:read"
		}
		return "goals:write"
	}
	if strings.HasPrefix(path, "/v1/dev/user/folders") || strings.HasPrefix(path, "/v1/dev/user/daily-summaries") {
		return "conversations:read"
	}
	if strings.HasPrefix(path, "/v1/dev/user/conversations") || strings.HasPrefix(path, "/v1/conversations/") {
		if method == http.MethodGet {
			return "conversations:read"
		}
		return "conversations:write"
	}
	if strings.HasPrefix(path, "/v1/integrations/") || strings.HasPrefix(path, "/v1/task-integrations/") {
		if method == http.MethodGet {
			return "conversations:read"
		}
		return "conversations:write"
	}
	return ""
}

type Handler struct{ Service Service }

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	v, e := h.Service.List(r.Context(), uid)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	jsonOut(w, v)
}
func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var in struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	v, raw, e := h.Service.Create(r.Context(), uid, in.Name, in.Scopes)
	if e != nil {
		code := 422
		if strings.HasPrefix(e.Error(), "invalid developer") || strings.Contains(e.Error(), "scope") {
			code = 400
		}
		http.Error(w, e.Error(), code)
		return
	}
	jsonOut(w, map[string]any{"id": v.ID, "name": v.Name, "key_prefix": v.KeyPrefix, "created_at": v.CreatedAt, "last_used_at": v.LastUsedAt, "scopes": v.Scopes, "key": raw})
}
func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	e = h.Service.Delete(r.Context(), uid, r.PathValue("key_id"))
	if errors.Is(e, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 503)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
