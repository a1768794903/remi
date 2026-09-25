package screenactivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/auth"
)

const rowLimit = 5000

type Service struct{ DB *sql.DB }
type row struct {
	ID, Timestamp, App, Title, OCR, DeviceName, ClientDeviceID string
	CaptureEligible                                            bool
}
type appSummary struct {
	Count        int      `json:"count"`
	FirstSeen    string   `json:"first_seen,omitempty"`
	LastSeen     string   `json:"last_seen,omitempty"`
	WindowTitles []string `json:"window_titles"`
}
type summary struct {
	Apps      map[string]appSummary `json:"apps"`
	Total     int                   `json:"total_screenshots"`
	Truncated bool                  `json:"-"`
	Coverage  map[string]any        `json:"coverage"`
}

func normalizeTimestamp(value string, endOfSecond bool) (string, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("screen activity timestamp must be ISO-8601 compatible")
	}
	parsed = parsed.UTC()
	milliseconds := parsed.Nanosecond() / 1e6
	if endOfSecond && parsed.Nanosecond() == 0 {
		milliseconds = 999
	}
	return parsed.Format("2006-01-02 15:04:05") + "." + twoOrThree(milliseconds), nil
}
func twoOrThree(v int) string {
	return strconv.Itoa(v/100) + strconv.Itoa((v/10)%10) + strconv.Itoa(v%10)
}
func summarize(rows []row, limit int) summary {
	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	out := summary{Apps: map[string]appSummary{}, Total: len(rows), Truncated: truncated, Coverage: map[string]any{"source": "synced_screen_activity", "row_limit": limit, "truncated": truncated, "capture_completeness": "unknown"}}
	if len(rows) > 0 {
		out.Coverage["first_observed_at"] = rows[0].Timestamp
		out.Coverage["last_observed_at"] = rows[len(rows)-1].Timestamp
	}
	for _, item := range rows {
		app := item.App
		if app == "" {
			app = "Unknown"
		}
		entry := out.Apps[app]
		entry.Count++
		if entry.FirstSeen == "" {
			entry.FirstSeen = item.Timestamp
		}
		entry.LastSeen = item.Timestamp
		if item.Title != "" && len(entry.WindowTitles) < 10 {
			exists := false
			for _, title := range entry.WindowTitles {
				if title == item.Title {
					exists = true
				}
			}
			if !exists {
				entry.WindowTitles = append(entry.WindowTitles, item.Title)
			}
		}
		out.Apps[app] = entry
	}
	return out
}

func (s Service) Sync(ctx context.Context, uid string, rows []row, accountGeneration int, retention *int) (int, error) {
	if s.DB == nil {
		return 0, sql.ErrConnDone
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, item := range rows {
		_, err = tx.ExecContext(ctx, "INSERT INTO screen_activity (user_external_uid,storage_id,local_screenshot_id,timestamp,app_name,window_title,ocr_text,device_name,client_device_id,capture_eligible,account_generation,device_retention_seconds,created_at,updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE timestamp=VALUES(timestamp), app_name=VALUES(app_name), window_title=VALUES(window_title), ocr_text=VALUES(ocr_text), capture_eligible=VALUES(capture_eligible), account_generation=VALUES(account_generation), device_retention_seconds=VALUES(device_retention_seconds), updated_at=UTC_TIMESTAMP(6)", uid, item.ID, item.ID, item.Timestamp, item.App, item.Title, item.OCR, item.DeviceName, item.ClientDeviceID, item.CaptureEligible, accountGeneration, retention)
		if err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(rows), nil
}
func (s Service) List(ctx context.Context, uid, date, app string, limit int) ([]row, error) {
	if limit < 1 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	q := "SELECT storage_id,timestamp,app_name,window_title,ocr_text,COALESCE(device_name,''),COALESCE(client_device_id,''),capture_eligible FROM screen_activity WHERE user_external_uid = ?"
	args := []any{uid}
	if date != "" {
		start, _ := time.Parse("2006-01-02", date)
		q += " AND timestamp >= ? AND timestamp <= ?"
		args = append(args, start.UTC().Format("2006-01-02 15:04:05.000"), start.UTC().Add(24*time.Hour-time.Millisecond).Format("2006-01-02 15:04:05.000"))
	}
	if app != "" {
		q += " AND app_name = ?"
		args = append(args, app)
	}
	q += " ORDER BY timestamp ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.ID, &item.Timestamp, &item.App, &item.Title, &item.OCR, &item.DeviceName, &item.ClientDeviceID, &item.CaptureEligible); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

type Handler struct{ Service Service }

func (h Handler) Sync(w http.ResponseWriter, r *http.Request) {
	uid, ok := user(w, r)
	if !ok {
		return
	}
	var in struct {
		AccountGeneration int  `json:"account_generation"`
		Retention         *int `json:"deviceRetentionSeconds"`
		Rows              []struct {
			ID              int64  `json:"id"`
			Timestamp       string `json:"timestamp"`
			App             string `json:"appName"`
			Title           string `json:"windowTitle"`
			OCR             string `json:"ocrText"`
			DeviceName      string `json:"deviceName"`
			ClientDeviceID  string `json:"clientDeviceId"`
			CaptureEligible bool   `json:"captureEligible"`
		} `json:"rows"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Rows) > 100 {
		writeError(w, 400, "Maximum 100 rows per batch")
		return
	}
	items := make([]row, 0, len(in.Rows))
	lastID := int64(0)
	for _, raw := range in.Rows {
		stamp, err := normalizeTimestamp(raw.Timestamp, false)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if raw.ID > lastID {
			lastID = raw.ID
		}
		device := raw.ClientDeviceID
		storage := strconv.FormatInt(raw.ID, 10)
		if device != "" {
			storage = device + "-" + storage
		}
		items = append(items, row{ID: storage, Timestamp: stamp, App: raw.App[:minLen(len(raw.App), 512)], Title: raw.Title[:minLen(len(raw.Title), 2048)], OCR: raw.OCR[:minLen(len(raw.OCR), 8192)], DeviceName: raw.DeviceName, ClientDeviceID: device, CaptureEligible: raw.CaptureEligible})
	}
	written, err := h.Service.Sync(r.Context(), uid, items, in.AccountGeneration, in.Retention)
	if err != nil {
		writeError(w, 500, "database write failed")
		return
	}
	writeJSON(w, 200, map[string]any{"synced": written, "last_id": lastID})
}
func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, ok := user(w, r)
	if !ok {
		return
	}
	items, err := h.Service.List(r.Context(), uid, r.URL.Query().Get("date"), r.URL.Query().Get("app_filter"), atoiDefault(r.URL.Query().Get("limit"), 500))
	if err != nil {
		writeError(w, 500, "screen activity unavailable")
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{"id": item.ID, "timestamp": item.Timestamp, "appName": item.App, "windowTitle": item.Title})
	}
	writeJSON(w, 200, out)
}
func (h Handler) Summary(w http.ResponseWriter, r *http.Request) {
	uid, ok := user(w, r)
	if !ok {
		return
	}
	items, err := h.Service.List(r.Context(), uid, r.URL.Query().Get("date"), "", rowLimit+1)
	if err != nil {
		writeError(w, 500, "screen activity unavailable")
		return
	}
	writeJSON(w, 200, summarize(items, rowLimit))
}
func user(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := auth.UserID(r.Context())
	if err != nil {
		writeError(w, 401, err.Error())
		return "", false
	}
	return id, true
}
func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func atoiDefault(value string, fallback int) int {
	n, e := strconv.Atoi(value)
	if e != nil {
		return fallback
	}
	return n
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"detail": message})
}
