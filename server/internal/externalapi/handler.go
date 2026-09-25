package externalapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
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

func parseExternalDate(raw string, endOfDay bool) (time.Time, error) {
	raw = strings.TrimSpace(strings.Replace(raw, "Z", "+00:00", 1))
	if len(raw) == len("2006-01-02") {
		day, err := time.ParseInLocation("2006-01-02", raw, time.UTC)
		if err != nil {
			return time.Time{}, err
		}
		if endOfDay {
			return day.Add(24*time.Hour - time.Nanosecond), nil
		}
		return day, nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, err
	}
	return value, nil
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

func (h Handler) Tasks(w http.ResponseWriter, r *http.Request) {
	u, ok := h.auth(w, r)
	if !ok {
		return
	}
	if !h.requireAppCapability(w, r, "tasks") {
		return
	}
	limit, offset := page(r)
	clauses := []string{"u.external_uid=?", "a.status<>'cancelled'"}
	args := []any{u}
	if value := r.URL.Query().Get("completed"); value != "" {
		completed, err := strconv.ParseBool(value)
		if err != nil {
			http.Error(w, "completed must be a boolean", http.StatusBadRequest)
			return
		}
		if completed {
			clauses = append(clauses, "a.status='completed'")
		} else {
			clauses = append(clauses, "a.status<>'completed'")
		}
	}
	if conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id")); conversationID != "" {
		clauses = append(clauses, "CAST(a.conversation_id AS CHAR)=?")
		args = append(args, conversationID)
	}
	dateFilters := []struct {
		name, column string
		end          bool
	}{
		{"start_date", "a.created_at >= ?", false}, {"end_date", "a.created_at <= ?", true},
		{"due_start_date", "a.due_at >= ?", false}, {"due_end_date", "a.due_at <= ?", true},
	}
	for _, filter := range dateFilters {
		if raw := r.URL.Query().Get(filter.name); raw != "" {
			value, err := parseExternalDate(raw, filter.end)
			if err != nil {
				http.Error(w, "invalid "+filter.name, http.StatusBadRequest)
				return
			}
			clauses = append(clauses, filter.column)
			args = append(args, value)
		}
	}
	query := `SELECT CAST(a.id AS CHAR),a.description,a.status,a.owner,a.source,a.due_at,a.completed_at,a.created_at,a.updated_at,a.conversation_id,a.is_locked FROM action_items a JOIN users u ON u.id=a.user_id WHERE ` + strings.Join(clauses, " AND ") + ` ORDER BY a.created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := h.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, "failed to read tasks", http.StatusServiceUnavailable)
		return
	}
	defer rows.Close()
	tasks := []map[string]any{}
	for rows.Next() {
		var id, description, status, owner, source string
		var dueAt, completedAt, createdAt, updatedAt sql.NullTime
		var conversationID sql.NullInt64
		var locked bool
		if err := rows.Scan(&id, &description, &status, &owner, &source, &dueAt, &completedAt, &createdAt, &updatedAt, &conversationID, &locked); err != nil {
			continue
		}
		if locked && len(description) > 70 {
			description = description[:70] + "..."
		}
		item := map[string]any{"id": id, "description": description, "status": status, "completed": status == "completed", "owner": owner, "source": source, "is_locked": locked, "created_at": createdAt.Time, "updated_at": updatedAt.Time}
		if dueAt.Valid {
			item["due_at"] = dueAt.Time
		}
		if completedAt.Valid {
			item["completed_at"] = completedAt.Time
		}
		if conversationID.Valid {
			item["conversation_id"] = conversationID.Int64
		}
		tasks = append(tasks, item)
	}
	write(w, map[string]any{"tasks": tasks})
}

func (h Handler) Notification(w http.ResponseWriter, r *http.Request) {
	u, ok := h.auth(w, r)
	if !ok {
		return
	}
	if !h.requireAppCapability(w, r, "chat_messages") {
		return
	}
	message := strings.TrimSpace(r.URL.Query().Get("message"))
	if message == "" || len(message) > 4096 {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	appID := r.PathValue("app_id")
	var count int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM integration_notification_events WHERE app_id=? AND user_external_uid=? AND created_at>=UTC_TIMESTAMP()-INTERVAL 1 HOUR`, appID, u).Scan(&count); err != nil {
		http.Error(w, "notification storage unavailable", http.StatusServiceUnavailable)
		return
	}
	if count >= 10 {
		w.Header().Set("Retry-After", "3600")
		http.Error(w, "Rate limit exceeded. Maximum 10 notifications per hour.", http.StatusTooManyRequests)
		return
	}
	if _, err := h.DB.ExecContext(r.Context(), `INSERT INTO integration_notification_events(app_id,user_external_uid,message,source,created_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6))`, appID, u, message, "api.v2.integration"); err != nil {
		http.Error(w, "notification dispatch unavailable", http.StatusServiceUnavailable)
		return
	}
	write(w, map[string]string{"status": "Ok"})
}

func (h Handler) requireAppCapability(w http.ResponseWriter, r *http.Request, capability string) bool {
	var approved, disabled bool
	var raw []byte
	err := h.DB.QueryRowContext(r.Context(), `SELECT approved,disabled,COALESCE(capabilities,JSON_ARRAY()) FROM plugins_data WHERE id=?`, r.PathValue("app_id")).Scan(&approved, &disabled, &raw)
	if err != nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return false
	}
	if !approved || disabled {
		http.Error(w, "App not found", http.StatusNotFound)
		return false
	}
	var capabilities []string
	if json.Unmarshal(raw, &capabilities) == nil && len(capabilities) > 0 {
		allowed := map[string]bool{capability: true}
		if capability == "tasks" {
			allowed["read_tasks"] = true
		}
		if capability == "chat_messages" {
			allowed["proactive_notification"] = true
			allowed["chat"] = true
		}
		for _, value := range capabilities {
			if allowed[value] {
				return true
			}
		}
		http.Error(w, "App does not have the required capability", http.StatusForbidden)
		return false
	}
	return true
}
