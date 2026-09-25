package dailysummaries

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"remi/server/internal/auth"
	"remi/server/internal/chat"
)

type Service struct {
	DB    *sql.DB
	Redis *redis.Client
}

const generationCooldown = 30 * time.Second

func (s Service) cooldownActive(ctx context.Context, key string) bool {
	return s.Redis != nil && s.Redis.Exists(ctx, key).Val() > 0
}

func (s Service) armCooldown(ctx context.Context, key string) {
	if s.Redis != nil {
		_ = s.Redis.Set(ctx, key, "1", generationCooldown).Err()
	}
}

var allowedPublic = map[string]bool{"id": true, "date": true, "created_at": true, "headline": true, "overview": true, "day_emoji": true, "stats": true, "highlights": true, "action_items": true, "unresolved_questions": true, "decisions_made": true, "knowledge_nuggets": true, "memories_learned": true, "locations": true}

func validVisibility(value string) bool { return value == "shared" || value == "private" }

func publicFields(input map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range input {
		if allowedPublic[key] {
			out[key] = value
		}
	}
	return out
}

func (s Service) List(ctx context.Context, uid string, limit, offset int) ([]map[string]any, error) {
	if s.DB == nil {
		return nil, sql.ErrConnDone
	}
	if limit < 1 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT payload FROM daily_summaries WHERE user_external_uid = ? ORDER BY summary_date DESC LIMIT ? OFFSET ?", uid, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPayloads(rows)
}
func (s Service) Get(ctx context.Context, uid, id string) (map[string]any, error) {
	return s.one(ctx, "SELECT payload FROM daily_summaries WHERE user_external_uid = ? AND external_id = ?", uid, id)
}
func (s Service) ByDate(ctx context.Context, uid, date string) (map[string]any, error) {
	return s.one(ctx, "SELECT payload FROM daily_summaries WHERE user_external_uid = ? AND summary_date = ? LIMIT 1", uid, date)
}
func (s Service) Shared(ctx context.Context, id string) (map[string]any, error) {
	return s.one(ctx, "SELECT payload FROM daily_summaries WHERE external_id = ? AND visibility = 'shared'", id)
}
func (s Service) SetVisibility(ctx context.Context, uid, id, value string) error {
	if !validVisibility(value) {
		return errors.New("Invalid visibility value. Must be 'shared' or 'private'")
	}
	result, err := s.DB.ExecContext(ctx, "UPDATE daily_summaries SET visibility = ?, payload = JSON_SET(payload, '$.visibility', ?) WHERE user_external_uid = ? AND external_id = ?", value, value, uid, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s Service) Delete(ctx context.Context, uid, id string) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM daily_summaries WHERE user_external_uid = ? AND external_id = ?", uid, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s Service) one(ctx context.Context, q string, args ...any) (map[string]any, error) {
	if s.DB == nil {
		return nil, sql.ErrConnDone
	}
	var raw []byte
	err := s.DB.QueryRowContext(ctx, q, args...).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, errors.New("invalid daily summary payload")
	}
	return out, nil
}
func scanPayloads(rows *sql.Rows) ([]map[string]any, error) {
	out := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item map[string]any
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type Handler struct {
	Service  Service
	Provider chat.Provider
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, ok := uid(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, err := h.Service.List(r.Context(), uid, limit, offset)
	if err != nil {
		writeError(w, 500, "daily summaries unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"summaries": items})
}
func (h Handler) Item(w http.ResponseWriter, r *http.Request) {
	uid, ok := uid(w, r)
	if !ok {
		return
	}
	id := r.PathValue("summary_id")
	switch r.Method {
	case http.MethodGet:
		item, err := h.Service.Get(r.Context(), uid, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "Daily summary not found")
			return
		}
		if err != nil {
			writeError(w, 500, "daily summary unavailable")
			return
		}
		writeJSON(w, 200, item)
	case http.MethodDelete:
		err := h.Service.Delete(r.Context(), uid, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "Daily summary not found")
			return
		}
		if err != nil {
			writeError(w, 500, "failed to delete daily summary")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (h Handler) Visibility(w http.ResponseWriter, r *http.Request) {
	uid, ok := uid(w, r)
	if !ok {
		return
	}
	value := r.URL.Query().Get("value")
	if value == "" {
		value = r.FormValue("value")
	}
	err := h.Service.SetVisibility(r.Context(), uid, r.PathValue("summary_id"), value)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "Daily summary not found")
		return
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "Ok"})
}
func (h Handler) Shared(w http.ResponseWriter, r *http.Request) {
	item, err := h.Service.Shared(r.Context(), r.PathValue("summary_id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "Daily summary not found")
		return
	}
	if err != nil {
		writeError(w, 500, "daily summary unavailable")
		return
	}
	writeJSON(w, 200, publicFields(item))
}
func (h Handler) Generate(w http.ResponseWriter, r *http.Request) {
	uid, ok := uid(w, r)
	if !ok {
		return
	}
	if h.Provider == nil {
		writeError(w, http.StatusServiceUnavailable, "Daily summary generation is not configured in the Go service", "provider_not_configured")
		return
	}
	if h.Service.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "daily summary storage is not configured", "storage_not_configured")
		return
	}
	var input struct {
		Date string `json:"date"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)
	date := strings.TrimSpace(input.Date)
	existingID := r.PathValue("summary_id")
	var existingPayload map[string]any
	if existingID != "" {
		existing, err := h.Service.Get(r.Context(), uid, existingID)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "Daily summary not found")
			return
		}
		if err != nil {
			writeError(w, 500, "daily summary unavailable")
			return
		}
		existingPayload = existing
		if date == "" {
			if raw, ok := existing["date"].(string); ok {
				date = raw
			}
		}
		cooldownKey := "daily_summary_regen:" + uid + ":" + existingID
		if h.Service.cooldownActive(r.Context(), cooldownKey) {
			writeError(w, http.StatusTooManyRequests, "Please wait a few seconds before regenerating this recap again.")
			return
		}
		h.Service.armCooldown(r.Context(), cooldownKey)
	}
	zone := "UTC"
	var configuredZone sql.NullString
	if err := h.Service.DB.QueryRowContext(r.Context(), `SELECT time_zone FROM users WHERE external_uid=?`, uid).Scan(&configuredZone); err == nil && configuredZone.Valid && strings.TrimSpace(configuredZone.String) != "" {
		zone = strings.TrimSpace(configuredZone.String)
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		location = time.UTC
	}
	if date == "" {
		date = time.Now().In(location).Format("2006-01-02")
	}
	day, err := time.ParseInLocation("2006-01-02", date, location)
	if err != nil {
		writeError(w, 422, "Invalid date format. Use YYYY-MM-DD")
		return
	}
	nowLocal := time.Now().In(location)
	if day.After(time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, location)) {
		writeError(w, 422, "Date cannot be in the future")
		return
	}
	if existingID == "" {
		if existing, err := h.Service.ByDate(r.Context(), uid, date); err == nil {
			writeJSON(w, 200, existing)
			return
		}
		if h.Service.cooldownActive(r.Context(), "daily_summary_create:"+uid+":"+date) {
			writeError(w, http.StatusTooManyRequests, "Please wait a few seconds before regenerating this recap again.")
			return
		}
	}
	startUTC := day.UTC()
	endUTC := day.AddDate(0, 0, 1).UTC()
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT id,title,summary,started_at,ended_at FROM conversations c JOIN users u ON u.id=c.user_id WHERE u.external_uid=? AND c.started_at>=? AND c.started_at<? ORDER BY c.started_at ASC LIMIT 200`, uid, startUTC, endUTC)
	if err != nil {
		writeError(w, 500, "failed to load conversations")
		return
	}
	defer rows.Close()
	type convo struct {
		ID, Title, Summary string
		Started, Ended     time.Time
	}
	items := []convo{}
	for rows.Next() {
		var c convo
		var ended *time.Time
		if rows.Scan(&c.ID, &c.Title, &c.Summary, &c.Started, &ended) == nil {
			if ended != nil {
				c.Ended = *ended
			}
			items = append(items, c)
		}
	}
	if len(items) == 0 {
		message := fmt.Sprintf("Nothing to summarize for %s", date)
		if existingID != "" {
			message = fmt.Sprintf("No conversations found for %s", date)
		}
		writeError(w, 400, message)
		return
	}
	parts := make([]string, 0, len(items))
	for _, c := range items {
		parts = append(parts, fmt.Sprintf("[%s] %s\n%s", c.ID, c.Title, c.Summary))
	}
	prompt := "Create a daily summary from these conversations. Return JSON only with keys headline, overview, day_emoji, stats, highlights, action_items, unresolved_questions, decisions_made, knowledge_nuggets, memories_learned, locations. Date: " + date + "\n\n" + strings.Join(parts, "\n\n")
	answer, err := h.Provider.Complete(r.Context(), []chat.Turn{{Role: "system", Content: "You produce concise structured daily summaries. Return valid JSON only."}, {Role: "user", Content: prompt}})
	if err != nil {
		writeError(w, 502, "daily summary provider unavailable")
		return
	}
	var payload map[string]any
	if json.Unmarshal([]byte(answer), &payload) != nil {
		payload = map[string]any{"headline": "Your Daily Summary", "overview": strings.TrimSpace(answer), "day_emoji": "📅"}
	}
	payload["id"] = existingID
	if existingID == "" {
		existingID = "daily_" + uuid.NewString()
		payload["id"] = existingID
	}
	payload["date"] = date
	if existingID != "" {
		if visibility, ok := existingPayload["visibility"]; ok {
			payload["visibility"] = visibility
		}
		if created, ok := existingPayload["created_at"]; ok {
			payload["created_at"] = created
		} else {
			payload["created_at"] = time.Now().UTC()
		}
		payload["regenerated_at"] = time.Now().UTC()
	} else {
		payload["created_at"] = time.Now().UTC()
	}
	payload["updated_at"] = time.Now().UTC()
	raw, _ := json.Marshal(payload)
	now := time.Now().UTC()
	if _, err = h.Service.DB.ExecContext(r.Context(), `INSERT INTO daily_summaries(external_id,user_external_uid,summary_date,payload,created_at,updated_at) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE payload=VALUES(payload),updated_at=VALUES(updated_at)`, existingID, uid, date, raw, now, now); err != nil {
		writeError(w, 500, "failed to persist daily summary")
		return
	}
	if r.PathValue("summary_id") == "" {
		h.Service.armCooldown(r.Context(), "daily_summary_create:"+uid+":"+date)
	} else {
		h.Service.armCooldown(r.Context(), "daily_summary_regen:"+uid+":"+r.PathValue("summary_id"))
	}
	writeJSON(w, 200, payload)
}
func uid(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := auth.UserID(r.Context())
	if err != nil {
		writeError(w, 401, "missing authenticated user")
		return "", false
	}
	return id, true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, message string, reason ...string) {
	body := map[string]any{"detail": message}
	if len(reason) > 0 {
		body["reason"] = reason[0]
	}
	writeJSON(w, status, body)
}
