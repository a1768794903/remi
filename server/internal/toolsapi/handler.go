package toolsapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/actionitems"
	"remi/server/internal/auth"
	"remi/server/internal/conversations"
	"remi/server/internal/integrations"
	"remi/server/internal/memories"
)

type Handler struct {
	DB            *sql.DB
	Conversations conversations.Service
	Memories      memories.Service
	Actions       actionitems.Service
	Integrations  integrations.Service
	HTTP          *http.Client
}

func (h Handler) SearchChunks(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Query) == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	if in.Limit < 1 {
		in.Limit = 20
	}
	if in.Limit > 30 {
		in.Limit = 30
	}
	if h.DB == nil {
		writeFailure(w, "search_conversation_chunks")
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT t.conversation_id,c.title,t.text,t.created_at FROM transcript_segments t JOIN conversations c ON c.id=t.conversation_id JOIN users u ON u.id=c.user_id WHERE u.external_uid=? AND t.text LIKE ? ORDER BY t.created_at DESC LIMIT ?`, uid, "%"+in.Query+"%", in.Limit)
	if err != nil {
		writeFailure(w, "search_conversation_chunks")
		return
	}
	defer rows.Close()
	parts := []string{}
	sources := []any{}
	for i := 1; rows.Next(); i++ {
		var conversationID sql.NullInt64
		var title, text string
		var created time.Time
		if rows.Scan(&conversationID, &title, &text, &created) != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("Excerpt %d (relevance: 1.00):\n%s", i, text))
		sources = append(sources, map[string]any{"kind": "conversation", "source_id": conversationID.Int64, "title": title, "preview": text, "created_at": created.UTC().Format(time.RFC3339)})
	}
	if len(parts) == 0 {
		writeOK(w, "search_conversation_chunks", fmt.Sprintf("No transcript excerpts found matching '%s'.", in.Query))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tool_name": "search_conversation_chunks", "result_text": strings.Join(parts, "\n\n"), "is_error": false, "sources": sources})
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	path := r.URL.Path
	switch {
	case path == "/v1/tools/conversations" && r.Method == http.MethodGet:
		limit := intQuery(r, "limit", 20, 5000)
		offset := intQuery(r, "offset", 0, 5000)
		items, err := h.Conversations.List(r.Context(), uid, limit, offset)
		if err != nil {
			writeFailure(w, "get_conversations")
			return
		}
		writeOK(w, "get_conversations", items)
	case path == "/v1/tools/conversations/search" && r.Method == http.MethodPost:
		var in struct {
			Query string `json:"query"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Query) == "" {
			writeError(w, http.StatusBadRequest, "query is required")
			return
		}
		items, err := h.Conversations.Search(r.Context(), uid, in.Query, 20)
		if err != nil {
			writeFailure(w, "search_conversations")
			return
		}
		writeOK(w, "search_conversations", items)
	case path == "/v1/tools/memories" && r.Method == http.MethodGet:
		items, err := h.Memories.List(r.Context(), uid, intQuery(r, "limit", 50, 5000), intQuery(r, "offset", 0, 5000))
		if err != nil {
			writeFailure(w, "get_memories")
			return
		}
		writeOK(w, "get_memories", items)
	case path == "/v1/tools/memories/search" && r.Method == http.MethodPost:
		var in struct {
			Query string `json:"query"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Query) == "" {
			writeError(w, http.StatusBadRequest, "query is required")
			return
		}
		result, err := h.Memories.Search(r.Context(), uid, in.Query, 20, 0, false)
		if err != nil {
			writeFailure(w, "search_memories")
			return
		}
		writeOK(w, "search_memories", result.Items)
	case path == "/v1/tools/action-items" && r.Method == http.MethodGet:
		items, err := h.Actions.List(r.Context(), uid, intQuery(r, "limit", 50, 5000), intQuery(r, "offset", 0, 5000), nil)
		if err != nil {
			writeFailure(w, "get_action_items")
			return
		}
		writeOK(w, "get_action_items", items)
	case path == "/v1/tools/action-items" && r.Method == http.MethodPost:
		var in actionitems.CreateInput
		if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Description) == "" {
			writeError(w, http.StatusBadRequest, "description is required")
			return
		}
		if in.Owner == "" {
			in.Owner = "user"
		}
		if in.Status == "" {
			in.Status = "active"
		}
		if in.Source == "" {
			in.Source = "tools"
		}
		item, err := h.Actions.Create(r.Context(), uid, in)
		if err != nil {
			writeFailure(w, "create_action_item")
			return
		}
		writeOK(w, "create_action_item", item)
	case path == "/v1/tools/calendar-events" && r.Method == http.MethodPost:
		var in struct {
			Title       string   `json:"title"`
			StartTime   string   `json:"start_time"`
			EndTime     string   `json:"end_time"`
			Description string   `json:"description"`
			Location    string   `json:"location"`
			Attendees   []string `json:"attendees"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.StartTime) == "" || strings.TrimSpace(in.EndTime) == "" {
			writeError(w, http.StatusBadRequest, "title, start_time, and end_time are required")
			return
		}
		start, startErr := time.Parse(time.RFC3339, in.StartTime)
		end, endErr := time.Parse(time.RFC3339, in.EndTime)
		if startErr != nil || endErr != nil || !end.After(start) {
			writeError(w, http.StatusBadRequest, "start_time and end_time must be timezone-aware RFC3339 values with end after start")
			return
		}
		value, err := h.createCalendarEvent(r, uid, in.Title, start, end, in.Description, in.Location, in.Attendees)
		if err != nil {
			writeFailure(w, "create_calendar_event")
			return
		}
		writeOK(w, "create_calendar_event", value)
	case strings.HasPrefix(path, "/v1/tools/action-items/") && r.Method == http.MethodPatch:
		id := strings.TrimPrefix(path, "/v1/tools/action-items/")
		if id == "" || strings.Contains(id, "/") {
			writeError(w, http.StatusBadRequest, "invalid action item id")
			return
		}
		var in actionitems.UpdateInput
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		item, err := h.Actions.Update(r.Context(), uid, id, in)
		if errors.Is(err, actionitems.ErrNotFound) {
			writeError(w, http.StatusNotFound, "action item not found")
			return
		}
		if err != nil {
			writeFailure(w, "update_action_item")
			return
		}
		writeOK(w, "update_action_item", item)
	default:
		writeError(w, http.StatusNotFound, "tool endpoint not found")
	}
}

func (h Handler) createCalendarEvent(r *http.Request, uid, title string, start, end time.Time, description, location string, attendees []string) (any, error) {
	if h.Integrations.Client == nil {
		return nil, errors.New("integration storage is not configured")
	}
	integration, err := h.Integrations.Raw(r.Context(), uid, "google_calendar")
	if err != nil {
		return nil, err
	}
	token, _ := integration["access_token"].(string)
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("Google Calendar is not connected")
	}
	body := map[string]any{
		"summary":     title,
		"description": description,
		"location":    location,
		"start":       map[string]string{"dateTime": start.Format(time.RFC3339)},
		"end":         map[string]string{"dateTime": end.Format(time.RFC3339)},
	}
	if len(attendees) > 0 {
		list := make([]map[string]string, 0, len(attendees))
		for _, attendee := range attendees {
			if value := strings.TrimSpace(attendee); value != "" {
				list = append(list, map[string]string{"email": value})
			}
		}
		body["attendees"] = list
	}
	raw, _ := json.Marshal(body)
	client := h.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://www.googleapis.com/calendar/v3/calendars/primary/events?"+url.Values{"sendUpdates": []string{"all"}}.Encode(), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("calendar provider returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}
	var value any
	if err := json.Unmarshal(responseBody, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func intQuery(r *http.Request, key string, fallback, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || n < 0 {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}

func writeOK(w http.ResponseWriter, name string, value any) {
	raw, _ := json.Marshal(value)
	writeJSON(w, http.StatusOK, map[string]any{"tool_name": name, "result_text": string(raw), "is_error": false, "sources": []any{}})
}
func writeFailure(w http.ResponseWriter, name string) {
	writeJSON(w, http.StatusOK, map[string]any{"tool_name": name, "result_text": "Error executing tool", "is_error": true, "sources": []any{}})
}
func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
