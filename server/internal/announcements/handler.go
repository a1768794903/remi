package announcements

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"remi/server/internal/auth"
)

type Handler struct{ DB *sql.DB }

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (h Handler) admin(r *http.Request) bool {
	key := strings.TrimSpace(r.Header.Get("secret-key"))
	configured := os.Getenv("ADMIN_KEY")
	return configured != "" && key == configured
}
func (h Handler) list(r *http.Request, uid string, pending bool) ([]map[string]any, error) {
	q := `SELECT id,type,active,app_version,firmware_version,device_models,expires_at,targeting,display,content,created_at FROM announcements WHERE active=1`
	args := []any{}
	if pending {
		q += ` AND (expires_at IS NULL OR expires_at >= ?)`
		args = append(args, time.Now().UTC())
	}
	q += ` ORDER BY created_at DESC`
	rows, err := h.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, typ string
		var active bool
		var app, firmware, models, targeting, display, content sql.NullString
		var expires, created sql.NullTime
		if err := rows.Scan(&id, &typ, &active, &app, &firmware, &models, &expires, &targeting, &display, &content, &created); err != nil {
			return nil, err
		}
		if pending && uid != "" {
			var n int
			_ = h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM announcement_dismissals WHERE user_external_uid=? AND announcement_id=?`, uid, id).Scan(&n)
			if n > 0 {
				continue
			}
		}
		item := map[string]any{"id": id, "type": typ, "active": active, "content": map[string]any{}}
		if app.Valid {
			item["app_version"] = app.String
		}
		if firmware.Valid {
			item["firmware_version"] = firmware.String
		}
		if models.Valid {
			var value any
			_ = json.Unmarshal([]byte(models.String), &value)
			item["device_models"] = value
		}
		if targeting.Valid {
			var value any
			_ = json.Unmarshal([]byte(targeting.String), &value)
			item["targeting"] = value
		}
		if display.Valid {
			var value any
			_ = json.Unmarshal([]byte(display.String), &value)
			item["display"] = value
		}
		if content.Valid {
			var value any
			_ = json.Unmarshal([]byte(content.String), &value)
			item["content"] = value
		}
		if expires.Valid {
			item["expires_at"] = expires.Time
		}
		if created.Valid {
			item["created_at"] = created.Time
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (h Handler) Public(w http.ResponseWriter, r *http.Request) {
	v, e := h.list(r, "", false)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	writeJSON(w, 200, v)
}
func (h Handler) Pending(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	platform := r.URL.Query().Get("platform")
	trigger := r.URL.Query().Get("trigger")
	if platform != "ios" && platform != "android" {
		http.Error(w, "Platform must be 'ios' or 'android'", 400)
		return
	}
	if trigger != "app_launch" && trigger != "version_upgrade" && trigger != "firmware_upgrade" {
		http.Error(w, "invalid trigger", 400)
		return
	}
	v, e := h.list(r, uid, true)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	writeJSON(w, 200, v)
}
func (h Handler) Dismiss(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	_, e = h.DB.ExecContext(r.Context(), `INSERT INTO announcement_dismissals(user_external_uid,announcement_id,cta_clicked,created_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE cta_clicked=VALUES(cta_clicked)`, uid, r.PathValue("announcement_id"), false, time.Now().UTC())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"success": true, "message": "Announcement dismissed"})
}
func (h Handler) Admin(w http.ResponseWriter, r *http.Request) {
	if !h.admin(r) {
		http.Error(w, "You are not authorized to perform this action", 403)
		return
	}
	id := r.PathValue("announcement_id")
	var e error
	switch r.Method {
	case http.MethodGet:
		var typ string
		var body string
		e = h.DB.QueryRowContext(r.Context(), `SELECT type,content FROM announcements WHERE id=?`, id).Scan(&typ, &body)
		if e == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		var content any
		_ = json.Unmarshal([]byte(body), &content)
		writeJSON(w, 200, map[string]any{"id": id, "type": typ, "content": content})
	case http.MethodDelete:
		_, e := h.DB.ExecContext(r.Context(), `UPDATE announcements SET active=0 WHERE id=?`, id)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "message": "Announcement deactivated"})
	case http.MethodPost:
		var in struct {
			ID      string         `json:"id"`
			Type    string         `json:"type"`
			Content map[string]any `json:"content"`
			Active  *bool          `json:"active"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.ID == "" || in.Type == "" {
			http.Error(w, "invalid announcement", 400)
			return
		}
		raw, _ := json.Marshal(in.Content)
		active := true
		if in.Active != nil {
			active = *in.Active
		}
		_, e = h.DB.ExecContext(r.Context(), `INSERT INTO announcements(id,type,active,content,created_at) VALUES(?,?,?,?,?)`, in.ID, in.Type, active, raw, time.Now().UTC())
		if e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		writeJSON(w, 201, map[string]any{"id": in.ID, "type": in.Type, "active": active, "content": in.Content})
	case http.MethodPut:
		var in map[string]any
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if v, ok := in["active"].(bool); ok {
			_, e = h.DB.ExecContext(r.Context(), `UPDATE announcements SET active=? WHERE id=?`, v, id)
		}
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		writeJSON(w, 200, map[string]any{"id": id, "active": in["active"]})
	}
}
