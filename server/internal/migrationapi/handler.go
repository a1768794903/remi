package migrationapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"remi/server/internal/auth"
)

type Handler struct{ DB *sql.DB }

func (h Handler) user(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return "", false
	}
	if h.DB == nil {
		http.Error(w, "migration storage is not configured", 503)
		return "", false
	}
	return u, true
}
func validTarget(v string) bool { return strings.TrimSpace(v) == "enhanced" }
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (h Handler) Requests(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(w, r)
	if !ok {
		return
	}
	target := r.URL.Query().Get("target_level")
	if !validTarget(target) {
		http.Error(w, "Invalid target_level. Only migration to 'enhanced' is supported.", 400)
		return
	}
	rows, e := h.DB.QueryContext(r.Context(), `SELECT 'conversation',CAST(c.id AS CHAR) FROM conversations c JOIN users u ON u.id=c.user_id WHERE u.external_uid=? AND COALESCE(c.data_protection_level,'standard')<>? UNION ALL SELECT 'memory',CAST(m.id AS CHAR) FROM memories m JOIN users u ON u.id=m.user_id WHERE u.external_uid=? AND COALESCE(m.data_protection_level,'standard')<>? UNION ALL SELECT 'chat',CAST(m.external_id AS CHAR) FROM chat_messages m JOIN users u ON u.id=m.user_id WHERE u.external_uid=? AND COALESCE(m.data_protection_level,'standard')<>? ORDER BY 1,2`, u, target, u, target, u, target)
	if e != nil {
		http.Error(w, "failed to inspect migration state", 503)
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var typ, id string
		if rows.Scan(&typ, &id) == nil {
			out = append(out, map[string]string{"type": typ, "id": id, "target_level": target})
		}
	}
	write(w, map[string]any{"needs_migration": out})
}

func (h Handler) Mutate(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(w, r)
	if !ok {
		return
	}
	var in struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Target   string `json:"target_level"`
		Requests []struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Target string `json:"target_level"`
		} `json:"requests"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		http.Error(w, "invalid migration request", 400)
		return
	}
	items := in.Requests
	if len(items) == 0 {
		items = []struct {
			ID     string `json:"id"`
			Type   string `json:"type"`
			Target string `json:"target_level"`
		}{{in.ID, in.Type, in.Target}}
	}
	if len(items) == 1 && items[0].ID == "" && items[0].Type == "" {
		if !validTarget(in.Target) {
			http.Error(w, "Invalid target_level. Only migration to 'enhanced' is supported.", 400)
			return
		}
		_, e := h.DB.ExecContext(r.Context(), `UPDATE users SET data_protection_level=? WHERE external_uid=?`, in.Target, u)
		if e != nil {
			http.Error(w, "failed to start migration", 503)
			return
		}
		write(w, map[string]any{"status": "ok", "message": "Migration status set."})
		return
	}
	tx, e := h.DB.BeginTx(r.Context(), nil)
	if e != nil {
		http.Error(w, "migration unavailable", 503)
		return
	}
	defer tx.Rollback()
	for _, item := range items {
		if !validTarget(item.Target) || strings.TrimSpace(item.ID) == "" {
			http.Error(w, "invalid migration item", 400)
			return
		}
		var q string
		var args []any
		switch item.Type {
		case "conversation":
			q = `UPDATE conversations c JOIN users u ON u.id=c.user_id SET c.data_protection_level=? WHERE u.external_uid=? AND c.id=?`
			args = []any{item.Target, u, item.ID}
		case "memory":
			q = `UPDATE memories m JOIN users u ON u.id=m.user_id SET m.data_protection_level=? WHERE u.external_uid=? AND m.id=?`
			args = []any{item.Target, u, item.ID}
		case "chat":
			q = `UPDATE chat_messages m JOIN users u ON u.id=m.user_id SET m.data_protection_level=? WHERE u.external_uid=? AND m.external_id=?`
			args = []any{item.Target, u, item.ID}
		default:
			http.Error(w, "Unknown object type", 400)
			return
		}
		if _, e = tx.ExecContext(r.Context(), q, args...); e != nil {
			http.Error(w, "failed to migrate "+item.Type, 503)
			return
		}
	}
	if e = tx.Commit(); e != nil {
		http.Error(w, "failed to commit migration", 503)
		return
	}
	write(w, map[string]string{"status": "ok"})
}

func (h Handler) Batch(w http.ResponseWriter, r *http.Request) { h.Mutate(w, r) }
func (h Handler) Finalize(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(w, r)
	if !ok {
		return
	}
	var in struct {
		Target string `json:"target_level"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil || !validTarget(in.Target) {
		http.Error(w, "Invalid target_level. Only migration to 'enhanced' is supported.", 400)
		return
	}
	if _, e := h.DB.ExecContext(r.Context(), `UPDATE users SET data_protection_level=?,migration_status=NULL WHERE external_uid=?`, in.Target, u); e != nil {
		http.Error(w, "failed to finalize migration", 503)
		return
	}
	write(w, map[string]string{"status": "ok"})
}
