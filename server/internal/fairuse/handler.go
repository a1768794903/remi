package fairuse

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/auth"
)

const supportEmail = "team@basedhardware.com"

type Handler struct{ DB *sql.DB }

func (h Handler) Status(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		writeError(w, 401, err.Error())
		return
	}
	if h.DB == nil {
		writeError(w, 503, "fair-use storage is not configured")
		return
	}
	state, err := h.state(r, uid)
	if err != nil {
		writeError(w, 500, "fair-use state unavailable")
		return
	}
	writeJSON(w, map[string]any{"stage": state.stage, "case_ref": state.caseRef, "speech_hours_today": roundHours(state.dailyMS), "speech_hours_3day": roundHours(state.threeDayMS), "speech_hours_weekly": roundHours(state.weeklyMS), "limits": map[string]any{"daily_hours": limitHours("FAIR_USE_DAILY_SPEECH_MS", 4*3600000), "three_day_hours": limitHours("FAIR_USE_3DAY_SPEECH_MS", 10*3600000), "weekly_hours": limitHours("FAIR_USE_WEEKLY_SPEECH_MS", 20*3600000)}, "usage_pct": map[string]float64{"daily": pct(state.dailyMS, limitMS("FAIR_USE_DAILY_SPEECH_MS", 4*3600000)), "three_day": pct(state.threeDayMS, limitMS("FAIR_USE_3DAY_SPEECH_MS", 10*3600000)), "weekly": pct(state.weeklyMS, limitMS("FAIR_USE_WEEKLY_SPEECH_MS", 20*3600000))}, "dg_budget": map[string]any{"daily_limit_ms": state.dgLimitMS, "used_ms": state.dgUsedMS, "remaining_ms": max0(state.dgLimitMS - state.dgUsedMS), "exhausted": state.dgLimitMS > 0 && state.dgUsedMS >= state.dgLimitMS, "resets_at": state.dgResetsAt}, "message": message(state.stage, state.caseRef)})
}

func (h Handler) Admin(w http.ResponseWriter, r *http.Request) {
	if !h.admin(w, r) {
		return
	}
	if h.DB == nil {
		writeError(w, 503, "fair-use storage is not configured")
		return
	}
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if r.Method == http.MethodGet && path == "v1/admin/fair-use/flagged" {
		h.flagged(w, r)
		return
	}
	if len(parts) >= 5 && parts[0] == "v1" && parts[1] == "admin" && parts[2] == "fair-use" && parts[3] == "user" {
		uid := parts[4]
		if len(parts) == 5 {
			h.detail(w, r, uid)
			return
		}
		if len(parts) >= 6 && r.Method == http.MethodPost {
			switch parts[5] {
			case "reset":
				h.reset(w, r, uid)
				return
			case "set-stage":
				h.setStage(w, r, uid)
				return
			case "resolve-event":
				if len(parts) >= 7 {
					h.resolve(w, r, uid, parts[6])
					return
				}
			}
		}
	}
	if len(parts) == 5 && parts[0] == "v1" && parts[1] == "admin" && parts[2] == "fair-use" && parts[3] == "case" {
		h.caseLookup(w, r, parts[4])
		return
	}
	writeError(w, 404, "not found")
}

type stateRow struct {
	stage, caseRef                                     string
	dailyMS, threeDayMS, weeklyMS, dgLimitMS, dgUsedMS int64
	dgResetsAt                                         any
}

func (h Handler) state(r *http.Request, uid string) (stateRow, error) {
	var s stateRow
	err := h.DB.QueryRowContext(r.Context(), `SELECT stage,COALESCE(case_ref,''),COALESCE(daily_speech_ms,0),COALESCE(three_day_speech_ms,0),COALESCE(weekly_speech_ms,0),COALESCE(dg_daily_limit_ms,0),COALESCE(dg_used_ms,0),dg_resets_at FROM fair_use_state WHERE user_external_uid=?`, uid).Scan(&s.stage, &s.caseRef, &s.dailyMS, &s.threeDayMS, &s.weeklyMS, &s.dgLimitMS, &s.dgUsedMS, &s.dgResetsAt)
	if err == sql.ErrNoRows {
		s.stage = "none"
		return s, nil
	}
	return s, err
}

func (h Handler) flagged(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	stage := r.URL.Query().Get("stage")
	query := `SELECT user_external_uid,stage,COALESCE(case_ref,''),updated_at FROM fair_use_state WHERE stage<> 'none'`
	args := []any{}
	if stage != "" {
		query += ` AND stage=?`
		args = append(args, stage)
	}
	query += ` ORDER BY updated_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := h.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "fair-use query failed")
		return
	}
	defer rows.Close()
	users := []map[string]any{}
	for rows.Next() {
		var uid, st, ref string
		var updated time.Time
		if rows.Scan(&uid, &st, &ref, &updated) == nil {
			users = append(users, map[string]any{"uid": uid, "stage": st, "case_ref": ref, "updated_at": updated})
		}
	}
	writeJSON(w, map[string]any{"users": users, "fair_use_enabled": true})
}

func (h Handler) detail(w http.ResponseWriter, r *http.Request, uid string) {
	s, err := h.state(r, uid)
	if err != nil {
		writeError(w, 500, "fair-use state unavailable")
		return
	}
	events := []map[string]any{}
	rows, e := h.DB.QueryContext(r.Context(), `SELECT event_id,case_ref,stage,notes,created_at,resolved_at FROM fair_use_events WHERE user_external_uid=? ORDER BY created_at DESC LIMIT 50`, uid)
	if e == nil {
		defer rows.Close()
		for rows.Next() {
			var id, ref, st, notes string
			var created, resolved any
			if rows.Scan(&id, &ref, &st, &notes, &created, &resolved) == nil {
				events = append(events, map[string]any{"event_id": id, "case_ref": ref, "stage": st, "notes": notes, "created_at": created, "resolved_at": resolved})
			}
		}
	}
	writeJSON(w, map[string]any{"uid": uid, "state": map[string]any{"stage": s.stage, "case_ref": s.caseRef}, "events": events, "current_speech_ms": map[string]int64{"daily_ms": s.dailyMS, "three_day_ms": s.threeDayMS, "weekly_ms": s.weeklyMS}})
}
func (h Handler) reset(w http.ResponseWriter, r *http.Request, uid string) {
	if _, e := h.DB.ExecContext(r.Context(), `INSERT INTO fair_use_state(user_external_uid,stage,case_ref,daily_speech_ms,three_day_speech_ms,weekly_speech_ms,dg_daily_limit_ms,dg_used_ms,updated_at) VALUES(?, 'none','',0,0,0,0,0,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE stage='none',case_ref='',daily_speech_ms=0,three_day_speech_ms=0,weekly_speech_ms=0,dg_used_ms=0,updated_at=UTC_TIMESTAMP(6)`, uid); e != nil {
		writeError(w, 500, "fair-use reset failed")
		return
	}
	writeJSON(w, map[string]string{"status": "reset"})
}
func (h Handler) setStage(w http.ResponseWriter, r *http.Request, uid string) {
	stage := r.URL.Query().Get("stage")
	if stage != "none" && stage != "warning" && stage != "throttle" && stage != "restrict" {
		writeError(w, 400, "invalid stage")
		return
	}
	if _, e := h.DB.ExecContext(r.Context(), `INSERT INTO fair_use_state(user_external_uid,stage,updated_at) VALUES(?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE stage=VALUES(stage),updated_at=UTC_TIMESTAMP(6)`, uid, stage); e != nil {
		writeError(w, 500, "fair-use update failed")
		return
	}
	writeJSON(w, map[string]string{"status": "updated", "stage": stage})
}
func (h Handler) resolve(w http.ResponseWriter, r *http.Request, uid, event string) {
	notes := r.URL.Query().Get("notes")
	if _, e := h.DB.ExecContext(r.Context(), `UPDATE fair_use_events SET resolved_at=UTC_TIMESTAMP(6),resolved_by=?,notes=? WHERE user_external_uid=? AND event_id=?`, r.Header.Get("X-Admin-Key"), notes, uid, event); e != nil {
		writeError(w, 500, "fair-use event update failed")
		return
	}
	writeJSON(w, map[string]string{"status": "resolved"})
}
func (h Handler) caseLookup(w http.ResponseWriter, r *http.Request, ref string) {
	var uid, event, stage string
	e := h.DB.QueryRowContext(r.Context(), `SELECT user_external_uid,event_id,stage FROM fair_use_events WHERE case_ref=? ORDER BY created_at DESC LIMIT 1`, ref).Scan(&uid, &event, &stage)
	if e == sql.ErrNoRows {
		writeError(w, 404, "case not found")
		return
	}
	if e != nil {
		writeError(w, 500, "fair-use lookup failed")
		return
	}
	writeJSON(w, map[string]any{"uid": uid, "event_id": event, "case_ref": ref, "stage": stage, "support_email": supportEmail})
}

func (h Handler) PublicCaseStatus(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "fair-use storage is not configured")
		return
	}
	ref := strings.TrimSpace(r.PathValue("case_ref"))
	if ref == "" {
		writeError(w, http.StatusBadRequest, "case reference is required")
		return
	}
	var uid, stage string
	var createdAt, resolvedAt sql.NullTime
	err := h.DB.QueryRowContext(r.Context(), `SELECT e.user_external_uid,COALESCE(s.stage,e.stage),e.created_at,e.resolved_at FROM fair_use_events e LEFT JOIN fair_use_state s ON s.user_external_uid=e.user_external_uid WHERE e.case_ref=? ORDER BY e.created_at DESC LIMIT 1`, ref).Scan(&uid, &stage, &createdAt, &resolvedAt)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "case not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fair-use lookup failed")
		return
	}
	_ = uid
	updatedAt := createdAt
	if resolvedAt.Valid {
		updatedAt = resolvedAt
	}
	var createdValue any
	var updatedValue any
	if createdAt.Valid {
		createdValue = createdAt.Time
	}
	if updatedAt.Valid {
		updatedValue = updatedAt.Time
	}
	writeJSON(w, map[string]any{"case_ref": ref, "stage": stage, "message": message(stage, ref), "created_at": createdValue, "updated_at": updatedValue, "support_email": supportEmail})
}
func (h Handler) admin(w http.ResponseWriter, r *http.Request) bool {
	expected := os.Getenv("ADMIN_KEY")
	got := r.Header.Get("X-Admin-Key")
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(got)) != 1 {
		writeError(w, 403, "invalid admin key")
		return false
	}
	return true
}
func limitMS(key string, fallback int64) int64 {
	if v, e := strconv.ParseInt(os.Getenv(key), 10, 64); e == nil && v > 0 {
		return v
	}
	return fallback
}
func limitHours(key string, fallback int64) float64 { return float64(limitMS(key, fallback)) / 3600000 }
func pct(v, limit int64) float64 {
	if limit <= 0 {
		return 0
	}
	p := float64(v) * 100 / float64(limit)
	if p > 100 {
		return 100
	}
	return p
}
func roundHours(v int64) float64 { return float64(v) / 3600000 }
func max0(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
func message(stage, ref string) string {
	suffix := ""
	if ref != "" {
		suffix = fmt.Sprintf(" Your case reference is %s.", ref)
	}
	switch stage {
	case "warning":
		return "Your usage is higher than typical." + suffix
	case "throttle":
		return "Your transcription quality has been temporarily reduced due to high usage." + suffix
	case "restrict":
		return "Your cloud transcription is temporarily limited. On-device transcription continues normally." + suffix
	default:
		return "Your usage is within normal limits."
	}
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": detail})
}
