package stagedtasks

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"remi/server/internal/actionitems"
	"remi/server/internal/auth"
)

type Handler struct {
	DB      *sql.DB
	Actions actionitems.Service
}

type stagedItem struct {
	ID             string     `json:"id"`
	Description    string     `json:"description"`
	Completed      bool       `json:"completed"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DueAt          *time.Time `json:"due_at,omitempty"`
	Source         *string    `json:"source,omitempty"`
	Priority       *string    `json:"priority,omitempty"`
	Metadata       *string    `json:"metadata,omitempty"`
	Category       *string    `json:"category,omitempty"`
	RelevanceScore *int       `json:"relevance_score,omitempty"`
}

func (h Handler) uid(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return "", false
	}
	return u, true
}
func (h Handler) requireDB(w http.ResponseWriter) bool {
	if h.DB == nil {
		http.Error(w, "staged task storage is not configured", 503)
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.uid(w, r)
	if !ok || !h.requireDB(w) {
		return
	}
	var in struct {
		Description    string     `json:"description"`
		DueAt          *time.Time `json:"due_at"`
		Source         *string    `json:"source"`
		Priority       *string    `json:"priority"`
		Metadata       *string    `json:"metadata"`
		Category       *string    `json:"category"`
		RelevanceScore *int       `json:"relevance_score"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Description) == "" || len([]rune(in.Description)) > 5000 {
		http.Error(w, "invalid description", 400)
		return
	}
	if in.RelevanceScore != nil && (*in.RelevanceScore < 0 || *in.RelevanceScore > 1000) {
		http.Error(w, "invalid relevance_score", 422)
		return
	}
	id := "staged_" + uuid.NewString()
	now := time.Now().UTC()
	_, err := h.DB.ExecContext(r.Context(), `INSERT INTO staged_tasks(id,user_external_uid,description,completed,due_at,source,priority,metadata,category,relevance_score,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, uid, strings.TrimSpace(in.Description), false, in.DueAt, in.Source, in.Priority, in.Metadata, in.Category, in.RelevanceScore, now, now)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	item := stagedItem{ID: id, Description: strings.TrimSpace(in.Description), CreatedAt: now, UpdatedAt: now, DueAt: in.DueAt, Source: in.Source, Priority: in.Priority, Metadata: in.Metadata, Category: in.Category, RelevanceScore: in.RelevanceScore}
	writeJSON(w, 201, item)
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.uid(w, r)
	if !ok || !h.requireDB(w) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT id,description,completed,created_at,updated_at,due_at,source,priority,metadata,category,relevance_score FROM staged_tasks WHERE user_external_uid=? AND completed=FALSE ORDER BY relevance_score IS NULL,relevance_score DESC,created_at DESC LIMIT ? OFFSET ?`, uid, limit+1, offset)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	items := []stagedItem{}
	for rows.Next() {
		var x stagedItem
		if err := rows.Scan(&x.ID, &x.Description, &x.Completed, &x.CreatedAt, &x.UpdatedAt, &x.DueAt, &x.Source, &x.Priority, &x.Metadata, &x.Category, &x.RelevanceScore); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		items = append(items, x)
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items, "has_more": more})
}

func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.uid(w, r)
	if !ok || !h.requireDB(w) {
		return
	}
	if r.PathValue("task_id") == "" {
		_, err := h.DB.ExecContext(r.Context(), `DELETE FROM staged_tasks WHERE user_external_uid=?`, uid)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, map[string]any{"status": "ok"})
		return
	}
	_, err := h.DB.ExecContext(r.Context(), `DELETE FROM staged_tasks WHERE user_external_uid=? AND id=?`, uid, r.PathValue("task_id"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (h Handler) Scores(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.uid(w, r)
	if !ok || !h.requireDB(w) {
		return
	}
	var in struct {
		Scores []struct {
			ID    string `json:"id"`
			Score int    `json:"relevance_score"`
		} `json:"scores"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Scores) > 500 {
		http.Error(w, "invalid scores", 400)
		return
	}
	for _, x := range in.Scores {
		if x.Score < 0 || x.Score > 1000 {
			http.Error(w, "invalid score", 422)
			return
		}
		if _, err := h.DB.ExecContext(r.Context(), `UPDATE staged_tasks SET relevance_score=?,updated_at=UTC_TIMESTAMP(6) WHERE user_external_uid=? AND id=?`, x.Score, uid, x.ID); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (h Handler) Promote(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.uid(w, r)
	if !ok || !h.requireDB(w) {
		return
	}
	id := r.PathValue("task_id")
	var x stagedItem
	q := `SELECT id,description,completed,due_at,created_at,updated_at FROM staged_tasks WHERE user_external_uid=? AND completed=FALSE `
	args := []any{uid}
	if id != "" {
		q += `AND id=? `
		args = append(args, id)
	}
	q += `ORDER BY relevance_score IS NULL,relevance_score DESC,created_at ASC LIMIT 1`
	err := h.DB.QueryRowContext(r.Context(), q, args...).Scan(&x.ID, &x.Description, &x.Completed, &x.DueAt, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 200, map[string]any{"promoted": false, "reason": "No staged tasks available", "promoted_task": nil})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	item, err := h.Actions.Create(r.Context(), uid, actionitems.CreateInput{Description: x.Description, Status: "active", Owner: "user", Source: "staged_task", DueAt: x.DueAt})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if _, err = h.DB.ExecContext(r.Context(), `UPDATE staged_tasks SET completed=TRUE,updated_at=UTC_TIMESTAMP(6) WHERE user_external_uid=? AND id=?`, uid, x.ID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]any{"promoted": true, "reason": nil, "promoted_task": item})
}

func (h Handler) Compatibility(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.uid(w, r); !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "migrated": 0, "deleted": 0, "restored": 0, "skipped_existing": 0, "has_more": false, "next_cursor": nil})
}
