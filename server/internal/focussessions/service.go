package focussessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"remi/server/internal/auth"
)

type Service struct{ DB *sql.DB }
type session struct {
	ID, Status, App, Description, Message string
	CreatedAt                             time.Time
	Duration                              int64
}
type distraction struct {
	AppOrSite    string `json:"app_or_site"`
	TotalSeconds int64  `json:"total_seconds"`
	Count        int    `json:"count"`
}
type statResult struct {
	Date              string        `json:"date"`
	FocusedMinutes    int64         `json:"focused_minutes"`
	DistractedMinutes int64         `json:"distracted_minutes"`
	SessionCount      int           `json:"session_count"`
	FocusedCount      int           `json:"focused_count"`
	DistractedCount   int           `json:"distracted_count"`
	Top               []distraction `json:"top_distractions"`
}

func stats(date string, items []session) statResult {
	result := statResult{Date: date}
	byApp := map[string]*distraction{}
	for _, item := range items {
		switch item.Status {
		case "focused":
			result.FocusedCount++
			result.FocusedMinutes += item.Duration / 60
		case "distracted":
			result.DistractedCount++
			duration := item.Duration
			if duration == 0 {
				duration = 60
			}
			result.DistractedMinutes += duration / 60
			entry := byApp[item.App]
			if entry == nil {
				entry = &distraction{AppOrSite: item.App}
				byApp[item.App] = entry
			}
			entry.TotalSeconds += duration
			entry.Count++
		}
	}
	result.SessionCount = result.FocusedCount + result.DistractedCount
	for _, entry := range byApp {
		result.Top = append(result.Top, *entry)
	}
	sortDistractions(result.Top)
	if len(result.Top) > 5 {
		result.Top = result.Top[:5]
	}
	return result
}

func (s Service) Create(ctx context.Context, uid, status, app, description, message string, duration *int64) (map[string]any, error) {
	if s.DB == nil {
		return nil, sql.ErrConnDone
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	_, err := s.DB.ExecContext(ctx, "INSERT INTO focus_sessions (external_id,user_external_uid,status,app_or_site,description,message,duration_seconds,created_at,updated_at) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?)", id, uid, status, app, description, message, duration, now, now)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "status": status, "app_or_site": app, "description": description, "message": nullableString(message), "created_at": now, "duration_seconds": duration}, nil
}
func (s Service) List(ctx context.Context, uid, date string, limit, offset int) ([]map[string]any, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	q := "SELECT external_id,status,app_or_site,description,COALESCE(message,''),created_at,COALESCE(duration_seconds,0) FROM focus_sessions WHERE user_external_uid = ?"
	args := []any{uid}
	if date != "" {
		q += " AND created_at >= ? AND created_at < ?"
		start, _ := time.Parse("2006-01-02", date)
		args = append(args, start.UTC(), start.UTC().AddDate(0, 0, 1))
	}
	q += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var item session
		if err := rows.Scan(&item.ID, &item.Status, &item.App, &item.Description, &item.Message, &item.CreatedAt, &item.Duration); err != nil {
			return nil, err
		}
		out = append(out, snapshot(item))
	}
	return out, rows.Err()
}
func (s Service) Delete(ctx context.Context, uid, id string) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM focus_sessions WHERE user_external_uid = ? AND external_id = ?", uid, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s Service) Stats(ctx context.Context, uid, date string) (statResult, error) {
	items, err := s.List(ctx, uid, date, 5000, 0)
	if err != nil {
		return statResult{}, err
	}
	typed := make([]session, 0, len(items))
	for _, raw := range items {
		duration, _ := raw["duration_seconds"].(int64)
		if duration == 0 {
			if number, ok := raw["duration_seconds"].(float64); ok {
				duration = int64(number)
			}
		}
		typed = append(typed, session{Status: raw["status"].(string), App: raw["app_or_site"].(string), Duration: duration})
	}
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	return stats(date, typed), nil
}

type Handler struct{ Service Service }

func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid, ok := user(w, r)
	if !ok {
		return
	}
	var in struct {
		Status      string  `json:"status"`
		App         string  `json:"app_or_site"`
		Description string  `json:"description"`
		Message     *string `json:"message"`
		Duration    *int64  `json:"duration_seconds"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || (in.Status != "focused" && in.Status != "distracted") || strings.TrimSpace(in.App) == "" || strings.TrimSpace(in.Description) == "" || (in.Duration != nil && (*in.Duration < 0 || *in.Duration > 86400)) {
		writeError(w, 400, "invalid focus session")
		return
	}
	message := ""
	if in.Message != nil {
		message = *in.Message
	}
	item, err := h.Service.Create(r.Context(), uid, in.Status, in.App, in.Description, message, in.Duration)
	if err != nil {
		writeError(w, 500, "failed to create focus session")
		return
	}
	writeJSON(w, 200, item)
}
func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, ok := user(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, err := h.Service.List(r.Context(), uid, r.URL.Query().Get("date"), limit, offset)
	if err != nil {
		writeError(w, 500, "failed to list focus sessions")
		return
	}
	writeJSON(w, 200, items)
}
func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	uid, ok := user(w, r)
	if !ok {
		return
	}
	err := h.Service.Delete(r.Context(), uid, r.PathValue("session_id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "focus session not found")
		return
	}
	if err != nil {
		writeError(w, 500, "failed to delete focus session")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
func (h Handler) Stats(w http.ResponseWriter, r *http.Request) {
	uid, ok := user(w, r)
	if !ok {
		return
	}
	item, err := h.Service.Stats(r.Context(), uid, r.URL.Query().Get("date"))
	if err != nil {
		writeError(w, 500, "failed to calculate focus stats")
		return
	}
	writeJSON(w, 200, item)
}
func user(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := auth.UserID(r.Context())
	if err != nil {
		writeError(w, 401, err.Error())
		return "", false
	}
	return id, true
}
func snapshot(item session) map[string]any {
	var message any
	if item.Message != "" {
		message = item.Message
	}
	return map[string]any{"id": item.ID, "status": item.Status, "app_or_site": item.App, "description": item.Description, "message": message, "created_at": item.CreatedAt, "duration_seconds": item.Duration}
}
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func sortDistractions(items []distraction) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].TotalSeconds > items[j-1].TotalSeconds; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"detail": message})
}
