package advice

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"remi/server/internal/auth"
)

type Handler struct{ DB *sql.DB }

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if h.DB == nil {
		http.Error(w, "advice storage is not configured", 503)
		return
	}
	if r.URL.Path == "/v1/advice/mark-all-read" && r.Method == http.MethodPost {
		h.delete(w, r, uid)
		return
	}
	switch r.Method {
	case http.MethodPost:
		h.create(w, r, uid)
	case http.MethodGet:
		h.list(w, r, uid)
	case http.MethodPatch:
		h.update(w, r, uid)
	case http.MethodDelete:
		h.delete(w, r, uid)
	}
}

type input struct {
	Content         string   `json:"content"`
	Category        *string  `json:"category"`
	Reasoning       *string  `json:"reasoning"`
	SourceApp       *string  `json:"source_app"`
	Confidence      *float64 `json:"confidence"`
	ContextSummary  *string  `json:"context_summary"`
	CurrentActivity *string  `json:"current_activity"`
	Read            *bool    `json:"is_read"`
	Dismissed       *bool    `json:"is_dismissed"`
}

func (h Handler) create(w http.ResponseWriter, r *http.Request, uid string) {
	var in input
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Content) < 1 || len(in.Content) > 10000 {
		http.Error(w, "invalid advice", 422)
		return
	}
	confidence := 0.5
	if in.Confidence != nil {
		confidence = *in.Confidence
	}
	if confidence < 0 || confidence > 1 {
		http.Error(w, "confidence must be between 0 and 1", 422)
		return
	}
	category := "other"
	if in.Category != nil && *in.Category != "" {
		category = *in.Category
	}
	idb := make([]byte, 16)
	_, _ = rand.Read(idb)
	now := time.Now().UTC()
	id := hex.EncodeToString(idb)
	_, err := h.DB.ExecContext(r.Context(), `INSERT INTO advice(id,user_external_uid,content,category,reasoning,source_app,confidence,context_summary,current_activity,is_read,is_dismissed,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, uid, in.Content, category, in.Reasoning, in.SourceApp, confidence, in.ContextSummary, in.CurrentActivity, false, false, now, now)
	if err != nil {
		http.Error(w, "failed to persist advice", 503)
		return
	}
	h.writeOne(w, r, id, uid)
}
func (h Handler) list(w http.ResponseWriter, r *http.Request, uid string) {
	limit := 100
	offset := 0
	if v, e := strconv.Atoi(r.URL.Query().Get("limit")); e == nil && v > 0 {
		if v > 1000 {
			v = 1000
		}
		limit = v
	}
	if v, e := strconv.Atoi(r.URL.Query().Get("offset")); e == nil && v >= 0 {
		offset = v
	}
	include := r.URL.Query().Get("include_dismissed") == "true"
	category := r.URL.Query().Get("category")
	q := `SELECT id,user_external_uid,content,category,reasoning,source_app,confidence,context_summary,current_activity,is_read,is_dismissed,created_at,updated_at FROM advice WHERE user_external_uid=?`
	args := []any{uid}
	if !include {
		q += ` AND is_dismissed=FALSE`
	}
	if category != "" {
		q += ` AND category=?`
		args = append(args, category)
	}
	q += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, e := h.DB.QueryContext(r.Context(), q, args...)
	if e != nil {
		http.Error(w, "failed to list advice", 503)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		v, e := scan(rows)
		if e != nil {
			http.Error(w, "failed to read advice", 503)
			return
		}
		out = append(out, v)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
func (h Handler) update(w http.ResponseWriter, r *http.Request, uid string) {
	id := r.PathValue("advice_id")
	var in input
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid advice", 422)
		return
	}
	_, e := h.DB.ExecContext(r.Context(), `UPDATE advice SET is_read=COALESCE(?,is_read),is_dismissed=COALESCE(?,is_dismissed),updated_at=? WHERE id=? AND user_external_uid=?`, in.Read, in.Dismissed, time.Now().UTC(), id, uid)
	if e != nil {
		http.Error(w, "failed to update advice", 503)
		return
	}
	h.writeOne(w, r, id, uid)
}
func (h Handler) delete(w http.ResponseWriter, r *http.Request, uid string) {
	if r.URL.Path == "/v1/advice/mark-all-read" {
		_, e := h.DB.ExecContext(r.Context(), `UPDATE advice SET is_read=TRUE,updated_at=? WHERE user_external_uid=? AND is_read=FALSE`, time.Now().UTC(), uid)
		if e != nil {
			http.Error(w, "failed to update advice", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	_, e := h.DB.ExecContext(r.Context(), `DELETE FROM advice WHERE id=? AND user_external_uid=?`, r.PathValue("advice_id"), uid)
	if e != nil {
		http.Error(w, "failed to delete advice", 503)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
func (h Handler) writeOne(w http.ResponseWriter, r *http.Request, id, uid string) {
	row := h.DB.QueryRowContext(r.Context(), `SELECT id,user_external_uid,content,category,reasoning,source_app,confidence,context_summary,current_activity,is_read,is_dismissed,created_at,updated_at FROM advice WHERE id=? AND user_external_uid=?`, id, uid)
	v, e := scan(row)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type scanner interface{ Scan(...any) error }

func scan(s scanner) (map[string]any, error) {
	var id, uid, content, category string
	var reasoning, source, context, activity sql.NullString
	var confidence float64
	var read, dismissed bool
	var created, updated time.Time
	e := s.Scan(&id, &uid, &content, &category, &reasoning, &source, &confidence, &context, &activity, &read, &dismissed, &created, &updated)
	if e != nil {
		return nil, e
	}
	return map[string]any{"id": id, "content": content, "category": category, "reasoning": nullable(reasoning), "source_app": nullable(source), "confidence": confidence, "context_summary": nullable(context), "current_activity": nullable(activity), "created_at": created, "updated_at": updated, "is_read": read, "is_dismissed": dismissed}, nil
}
func nullable(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return v.String
}
