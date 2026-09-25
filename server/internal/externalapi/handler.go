package externalapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type Handler struct{ DB *sql.DB }

func (h Handler) auth(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.DB == nil {
		http.Error(w, "integration storage is not configured", 503)
		return "", false
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		http.Error(w, "Missing or invalid Authorization header", 401)
		return "", false
	}
	sum := sha256.Sum256([]byte(parts[1]))
	var appID string
	if err := h.DB.QueryRowContext(r.Context(), `SELECT app_id FROM app_api_keys WHERE key_hash=?`, hex.EncodeToString(sum[:])).Scan(&appID); err != nil {
		http.Error(w, "Invalid integration API key", 403)
		return "", false
	}
	if appID != r.PathValue("app_id") {
		http.Error(w, "Invalid integration API key", 403)
		return "", false
	}
	u := strings.TrimSpace(r.URL.Query().Get("uid"))
	if u == "" {
		http.Error(w, "uid is required", 422)
		return "", false
	}
	var enabled int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT 1 FROM user_enabled_apps WHERE user_external_uid=? AND app_id=?`, u, appID).Scan(&enabled); err != nil {
		http.Error(w, "App is not enabled for this user", 403)
		return "", false
	}
	return u, true
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func page(r *http.Request) (int, int) {
	limit, offset := 100, 0
	if n, e := strconv.Atoi(r.URL.Query().Get("limit")); e == nil && n > 0 {
		limit = n
	}
	if limit > 1000 {
		limit = 1000
	}
	if n, e := strconv.Atoi(r.URL.Query().Get("offset")); e == nil && n >= 0 {
		offset = n
	}
	return limit, offset
}
func (h Handler) Memories(w http.ResponseWriter, r *http.Request) {
	u, ok := h.auth(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		var in struct {
			Text     string   `json:"text"`
			Memories []string `json:"memories"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || (strings.TrimSpace(in.Text) == "" && len(in.Memories) == 0) {
			http.Error(w, "Either text or explicit memories are required", 422)
			return
		}
		content := strings.TrimSpace(in.Text)
		if content == "" {
			content = strings.Join(in.Memories, "\n")
		}
		_, e := h.DB.ExecContext(r.Context(), `INSERT INTO memories(type,category,visibility,content,importance,created_at,updated_at,user_id) SELECT 'fact','interesting','private',?,50,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),id FROM users WHERE external_uid=?`, content, u)
		if e != nil {
			http.Error(w, "failed to create memory", 503)
			return
		}
		write(w, map[string]any{})
		return
	}
	limit, offset := page(r)
	rows, e := h.DB.QueryContext(r.Context(), `SELECT CAST(m.id AS CHAR),m.type,m.category,m.visibility,m.is_read,m.is_dismissed,m.content,m.importance,m.event_time,m.created_at,m.updated_at FROM memories m JOIN users u ON u.id=m.user_id WHERE u.external_uid=? ORDER BY m.created_at DESC LIMIT ? OFFSET ?`, u, limit, offset)
	if e != nil {
		http.Error(w, "failed to read memories", 503)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, typ, cat, vis, content string
		var read, dismiss bool
		var importance int
		var event, created, updated sql.NullTime
		if rows.Scan(&id, &typ, &cat, &vis, &read, &dismiss, &content, &importance, &event, &created, &updated) != nil {
			continue
		}
		item := map[string]any{"id": id, "type": typ, "category": cat, "visibility": vis, "is_read": read, "is_dismissed": dismiss, "content": content, "importance": importance, "created_at": created.Time, "updated_at": updated.Time}
		if event.Valid {
			item["event_time"] = event.Time
		}
		out = append(out, item)
	}
	write(w, map[string]any{"memories": out})
}
func (h Handler) Conversations(w http.ResponseWriter, r *http.Request) {
	u, ok := h.auth(w, r)
	if !ok {
		return
	}
	limit, offset := page(r)
	rows, e := h.DB.QueryContext(r.Context(), `SELECT CAST(c.id AS CHAR),c.title,c.summary,c.status,c.started_at,c.ended_at,c.created_at,c.updated_at FROM conversations c JOIN users u ON u.id=c.user_id WHERE u.external_uid=? ORDER BY c.started_at DESC LIMIT ? OFFSET ?`, u, limit, offset)
	if e != nil {
		http.Error(w, "failed to read conversations", 503)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, title, summary, status string
		var started, ended, created, updated sql.NullTime
		if rows.Scan(&id, &title, &summary, &status, &started, &ended, &created, &updated) != nil {
			continue
		}
		item := map[string]any{"id": id, "title": title, "summary": summary, "status": status, "started_at": started.Time, "created_at": created.Time, "updated_at": updated.Time}
		if ended.Valid {
			item["ended_at"] = ended.Time
		}
		out = append(out, item)
	}
	write(w, map[string]any{"conversations": out})
}
