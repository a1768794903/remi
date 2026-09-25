package feedbackadmin

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type Handler struct{ DB *sql.DB }

func (h Handler) admin(w http.ResponseWriter, r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("ADMIN_KEY"))
	provided := strings.TrimSpace(r.Header.Get("X-Admin-Key"))
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		http.Error(w, "Invalid admin key", 403)
		return false
	}
	if h.DB == nil {
		http.Error(w, "feedback storage is not configured", 503)
		return false
	}
	return true
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (h Handler) Dates(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	limit := 30
	if v, e := fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit); e != nil || v == 0 {
		limit = 30
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 90 {
		limit = 90
	}
	rows, e := h.DB.QueryContext(r.Context(), `SELECT DISTINCT DATE(updated_at) FROM chat_messages WHERE rating=-1 ORDER BY DATE(updated_at) DESC LIMIT ?`, limit)
	if e != nil {
		http.Error(w, "failed to list feedback reports", 503)
		return
	}
	defer rows.Close()
	dates := []string{}
	for rows.Next() {
		var day time.Time
		if rows.Scan(&day) == nil {
			dates = append(dates, day.Format("2006-01-02"))
		}
	}
	write(w, map[string]any{"dates": dates})
}

func (h Handler) Report(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	day := r.PathValue("report_date")
	if _, e := time.Parse("2006-01-02", day); e != nil {
		http.Error(w, "date must be YYYY-MM-DD", 400)
		return
	}
	start := day + " 00:00:00"
	end := day + " 23:59:59.999999"
	var total int
	if e := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM chat_messages WHERE rating=-1 AND updated_at BETWEEN ? AND ?`, start, end).Scan(&total); e != nil {
		http.Error(w, "failed to read feedback report", 503)
		return
	}
	rows, e := h.DB.QueryContext(r.Context(), `SELECT m.external_id,m.user_id,m.app_id,m.updated_at,m.report_reason,u.external_uid FROM chat_messages m JOIN users u ON u.id=m.user_id WHERE m.rating=-1 AND m.updated_at BETWEEN ? AND ? ORDER BY m.updated_at ASC LIMIT 500`, start, end)
	if e != nil {
		http.Error(w, "failed to read feedback report", 503)
		return
	}
	defer rows.Close()
	entries := []any{}
	surfaces := map[string]int{"chat_text": total}
	reasons := map[string]int{}
	platforms := map[string]int{}
	for rows.Next() {
		var id, uid string
		var userID int64
		var app sql.NullString
		var created time.Time
		var reason sql.NullString
		if rows.Scan(&id, &userID, &app, &created, &reason, &uid) != nil {
			continue
		}
		event := map[string]any{"id": id, "uid": uid, "surface": "chat_text", "target_kind": "chat_message", "target_id": id, "value": -1, "created_at": created}
		if app.Valid {
			event["app_id"] = app.String
		}
		if reason.Valid && reason.String != "" {
			event["reason"] = reason.String
			reasons[reason.String]++
		}
		entries = append(entries, map[string]any{"event": event, "context": map[string]any{"event_id": id, "target_kind": "chat_message", "target_id": id}})
	}
	write(w, map[string]any{"date": day, "generated_at": time.Now().UTC(), "total_negative": total, "counts_by_surface": surfaces, "counts_by_reason": reasons, "counts_by_platform": platforms, "entries": entries, "truncated": total > len(entries)})
}

func (h Handler) Generate(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	day := r.PathValue("report_date")
	if _, e := time.Parse("2006-01-02", day); e != nil {
		http.Error(w, "date must be YYYY-MM-DD", 400)
		return
	}
	var total int
	if e := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM chat_messages WHERE rating=-1 AND DATE(updated_at)=?`, day).Scan(&total); e != nil {
		http.Error(w, "failed to generate feedback report", 503)
		return
	}
	write(w, map[string]any{"date": day, "total_negative": total, "truncated": total > 500})
}

func (h Handler) GenerateYesterday(w http.ResponseWriter, r *http.Request) {
	r.SetPathValue("report_date", time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"))
	h.Generate(w, r)
}

func (h Handler) Context(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	id := r.PathValue("event_id")
	var uid, text string
	var created time.Time
	if e := h.DB.QueryRowContext(r.Context(), `SELECT u.external_uid,m.text,m.updated_at FROM chat_messages m JOIN users u ON u.id=m.user_id WHERE m.external_id=? AND m.rating=-1`, id).Scan(&uid, &text, &created); e != nil {
		http.NotFound(w, r)
		return
	}
	write(w, map[string]any{"event_id": id, "uid": uid, "target_kind": "chat_message", "target_id": id, "created_at": created, "turns": []map[string]any{{"role": "assistant", "text": text, "created_at": created}}})
}
