package agenttools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/actionitems"
	"remi/server/internal/auth"
	"remi/server/internal/integrations"
	"remi/server/internal/memories"
)

type Handler struct {
	Actions      actionitems.Service
	Memories     memories.Service
	Integrations integrations.Service
	HTTP         *http.Client
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

var coreTools = []tool{
	{Name: "search_action_items", Description: "Search the user's action items.", Parameters: objectSchema(map[string]any{"query": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}}, "query")},
	{Name: "create_action_item", Description: "Create an action item for the user.", Parameters: objectSchema(map[string]any{"description": map[string]any{"type": "string", "minLength": 1, "maxLength": 5000}, "due_at": map[string]any{"type": "string", "format": "date-time"}}, "description")},
	{Name: "search_memories", Description: "Search the user's saved memories.", Parameters: objectSchema(map[string]any{"query": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}}, "query")},
	{Name: "get_calendar_events_tool", Description: "Retrieve events from the user's Google Calendar.", Parameters: objectSchema(map[string]any{"time_min": map[string]any{"type": "string", "format": "date-time"}, "time_max": map[string]any{"type": "string", "format": "date-time"}, "max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}})},
	{Name: "create_calendar_event_tool", Description: "Create an event in the user's Google Calendar.", Parameters: objectSchema(map[string]any{"title": map[string]any{"type": "string", "minLength": 1}, "start_time": map[string]any{"type": "string", "format": "date-time"}, "end_time": map[string]any{"type": "string", "format": "date-time"}, "description": map[string]any{"type": "string"}, "location": map[string]any{"type": "string"}, "attendees": map[string]any{"type": "string"}}, "title", "start_time", "end_time")},
	{Name: "update_calendar_event_tool", Description: "Update an event in the user's Google Calendar.", Parameters: objectSchema(map[string]any{"event_id": map[string]any{"type": "string"}, "event_title": map[string]any{"type": "string"}, "start_date": map[string]any{"type": "string", "format": "date-time"}, "end_date": map[string]any{"type": "string", "format": "date-time"}, "title": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"}, "location": map[string]any{"type": "string"}, "start_time": map[string]any{"type": "string", "format": "date-time"}, "end_time": map[string]any{"type": "string", "format": "date-time"}, "attendees": map[string]any{"type": "string"}})},
	{Name: "delete_calendar_event_tool", Description: "Delete an event from the user's Google Calendar.", Parameters: objectSchema(map[string]any{"event_id": map[string]any{"type": "string"}, "event_title": map[string]any{"type": "string"}, "start_date": map[string]any{"type": "string", "format": "date-time"}, "end_date": map[string]any{"type": "string", "format": "date-time"}})},
	{Name: "get_gmail_messages_tool", Description: "Retrieve messages from the user's Gmail account.", Parameters: objectSchema(map[string]any{"query": map[string]any{"type": "string"}, "max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}, "label": map[string]any{"type": "string"}})},
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func (h Handler) uid(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return "", false
	}
	return u, true
}
func write(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.uid(w, r); !ok {
		return
	}
	write(w, http.StatusOK, map[string]any{"tools": coreTools})
}

func (h Handler) Execute(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.uid(w, r)
	if !ok {
		return
	}
	var in struct {
		ToolName string         `json:"tool_name"`
		Params   map[string]any `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.ToolName) == "" {
		http.Error(w, "invalid tool request", http.StatusBadRequest)
		return
	}
	if in.Params == nil {
		in.Params = map[string]any{}
	}
	switch in.ToolName {
	case "search_action_items":
		query, _ := in.Params["query"].(string)
		limit := intParam(in.Params, "limit", 20, 50)
		items, err := h.Actions.Search(r.Context(), uid, query, limit)
		if err != nil {
			write(w, 200, map[string]any{"error": "Tool execution failed"})
			return
		}
		write(w, 200, map[string]any{"result": mustJSON(items)})
	case "create_action_item":
		description, _ := in.Params["description"].(string)
		if strings.TrimSpace(description) == "" {
			write(w, 200, map[string]any{"error": "Tool execution failed"})
			return
		}
		item, err := h.Actions.Create(r.Context(), uid, actionitems.CreateInput{Description: description, Status: "active", Owner: "user", Source: "agent"})
		if err != nil {
			write(w, 200, map[string]any{"error": "Tool execution failed"})
			return
		}
		write(w, 200, map[string]any{"result": mustJSON(item)})
	case "search_memories":
		query, _ := in.Params["query"].(string)
		limit := intParam(in.Params, "limit", 20, 50)
		result, err := h.Memories.Search(r.Context(), uid, query, limit, 0, false)
		if err != nil {
			write(w, 200, map[string]any{"error": "Tool execution failed"})
			return
		}
		write(w, 200, map[string]any{"result": mustJSON(result.Items)})
	case "get_calendar_events_tool":
		result, err := h.calendarEvents(r, uid, in.Params)
		if err != nil {
			write(w, 200, map[string]any{"error": "Tool execution failed"})
			return
		}
		write(w, 200, map[string]any{"result": result})
	case "create_calendar_event_tool":
		result, err := h.createCalendarEvent(r, uid, in.Params)
		if err != nil {
			write(w, 200, map[string]any{"error": "Tool execution failed"})
			return
		}
		write(w, 200, map[string]any{"result": result})
	case "update_calendar_event_tool":
		result, err := h.updateCalendarEvent(r, uid, in.Params)
		if err != nil {
			write(w, 200, map[string]any{"error": "Tool execution failed"})
			return
		}
		write(w, 200, map[string]any{"result": result})
	case "delete_calendar_event_tool":
		result, err := h.deleteCalendarEvent(r, uid, in.Params)
		if err != nil {
			write(w, 200, map[string]any{"error": "Tool execution failed"})
			return
		}
		write(w, 200, map[string]any{"result": result})
	case "get_gmail_messages_tool":
		result, err := h.gmailMessages(r, uid, in.Params)
		if err != nil {
			write(w, 200, map[string]any{"error": "Tool execution failed"})
			return
		}
		write(w, 200, map[string]any{"result": result})
	default:
		http.Error(w, "Tool '"+in.ToolName+"' not found", http.StatusNotFound)
	}
}

func (h Handler) providerClient() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (h Handler) integrationToken(ctx context.Context, uid string) (string, error) {
	if h.Integrations.Client == nil {
		return "", errors.New("integration storage is not configured")
	}
	data, err := h.Integrations.Raw(ctx, uid, "google_calendar")
	if err != nil {
		return "", err
	}
	token, _ := data["access_token"].(string)
	if strings.TrimSpace(token) == "" {
		return "", errors.New("Google integration is not connected")
	}
	return token, nil
}

func (h Handler) googleJSON(r *http.Request, token, endpoint string, method string, body []byte) (any, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.providerClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("google provider returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	if len(raw) == 0 {
		return map[string]any{"ok": true}, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func (h Handler) calendarEvents(r *http.Request, uid string, params map[string]any) (string, error) {
	token, err := h.integrationToken(r.Context(), uid)
	if err != nil {
		return "", err
	}
	value, err := h.calendarEventsValue(r, token, params)
	if err != nil {
		return "", err
	}
	return mustJSON(value), nil
}

func (h Handler) calendarEventsValue(r *http.Request, token string, params map[string]any) (any, error) {
	query := url.Values{}
	if v, ok := params["time_min"].(string); ok && strings.TrimSpace(v) != "" {
		query.Set("timeMin", v)
	}
	if v, ok := params["time_max"].(string); ok && strings.TrimSpace(v) != "" {
		query.Set("timeMax", v)
	}
	max := intParam(params, "max_results", 20, 50)
	query.Set("maxResults", strconv.Itoa(max))
	query.Set("singleEvents", "true")
	query.Set("orderBy", "startTime")
	value, err := h.googleJSON(r, token, "https://www.googleapis.com/calendar/v3/calendars/primary/events?"+query.Encode(), http.MethodGet, nil)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (h Handler) gmailMessages(r *http.Request, uid string, params map[string]any) (string, error) {
	token, err := h.integrationToken(r.Context(), uid)
	if err != nil {
		return "", err
	}
	if raw, rawErr := h.Integrations.Raw(r.Context(), uid, "google_calendar"); rawErr == nil && !googleIntegrationHasGmailScope(raw) {
		return "", errors.New("Gmail access has not been granted for this Google account")
	}
	query := url.Values{}
	if v, ok := params["query"].(string); ok && strings.TrimSpace(v) != "" {
		query.Set("q", v)
	}
	if label, ok := params["label"].(string); ok && strings.TrimSpace(label) != "" {
		if strings.EqualFold(label, "UNREAD") {
			old := query.Get("q")
			if old != "" {
				old += " "
			}
			query.Set("q", old+"is:unread")
		} else {
			query.Set("labelIds", strings.ToUpper(strings.TrimSpace(label)))
		}
	}
	query.Set("maxResults", strconv.Itoa(intParam(params, "max_results", 20, 50)))
	list, err := h.googleJSON(r, token, "https://gmail.googleapis.com/gmail/v1/users/me/messages?"+query.Encode(), http.MethodGet, nil)
	if err != nil {
		return "", err
	}
	obj, ok := list.(map[string]any)
	if !ok {
		return "", errors.New("invalid Gmail list response")
	}
	ids, _ := obj["messages"].([]any)
	parsed := make([]map[string]any, 0, len(ids))
	for _, item := range ids {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := entry["id"].(string)
		if id == "" {
			continue
		}
		full, err := h.googleJSON(r, token, "https://gmail.googleapis.com/gmail/v1/users/me/messages/"+url.PathEscape(id)+"?format=full", http.MethodGet, nil)
		if err != nil {
			return "", err
		}
		if message, ok := full.(map[string]any); ok {
			parsed = append(parsed, parseGmailMessage(message))
		}
	}
	if len(parsed) == 0 {
		info := ""
		if q := query.Get("q"); q != "" {
			info = " matching '" + q + "'"
		}
		return "No emails found" + info + ".", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Gmail Messages (%d found):\n\n", len(parsed))
	for i, message := range parsed {
		fmt.Fprintf(&b, "%d. %s\n", i+1, message["subject"])
		fmt.Fprintf(&b, "   From: %s\n   To: %s\n", message["from"], message["to"])
		if date := message["date"].(string); date != "" {
			fmt.Fprintf(&b, "   Date: %s\n", date)
		}
		if snippet := message["snippet"].(string); snippet != "" {
			if len(snippet) > 200 {
				snippet = snippet[:200] + "..."
			}
			fmt.Fprintf(&b, "   Preview: %s\n", snippet)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func googleIntegrationHasGmailScope(integration map[string]any) bool {
	raw, exists := integration["scopes"]
	if !exists || raw == nil {
		return true
	}
	switch scopes := raw.(type) {
	case []any:
		for _, item := range scopes {
			if scope, ok := item.(string); ok && scope == "https://www.googleapis.com/auth/gmail.readonly" {
				return true
			}
		}
	case []string:
		for _, scope := range scopes {
			if scope == "https://www.googleapis.com/auth/gmail.readonly" {
				return true
			}
		}
	case string:
		for _, scope := range strings.FieldsFunc(scopes, func(r rune) bool { return r == ',' || r == ' ' }) {
			if scope == "https://www.googleapis.com/auth/gmail.readonly" {
				return true
			}
		}
	}
	return false
}

func (h Handler) createCalendarEvent(r *http.Request, uid string, p map[string]any) (string, error) {
	token, err := h.integrationToken(r.Context(), uid)
	if err != nil {
		return "", err
	}
	title, _ := p["title"].(string)
	start, _ := p["start_time"].(string)
	end, _ := p["end_time"].(string)
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil || title == "" {
		return "", errors.New("invalid calendar event start/title")
	}
	endTime, err := time.Parse(time.RFC3339, end)
	if err != nil || !endTime.After(startTime) {
		return "", errors.New("invalid calendar event end")
	}
	event := calendarEventBody(p, startTime, endTime)
	value, err := h.googleJSON(r, token, "https://www.googleapis.com/calendar/v3/calendars/primary/events", http.MethodPost, event)
	if err != nil {
		return "", err
	}
	obj, _ := value.(map[string]any)
	link, _ := obj["htmlLink"].(string)
	result := fmt.Sprintf("✅ Successfully created calendar event: %s\n   Start: %s\n   End: %s", title, startTime.Format("2006-01-02 15:04:05 MST"), endTime.Format("2006-01-02 15:04:05 MST"))
	if location, _ := p["location"].(string); location != "" {
		result += "\n   Location: " + location
	}
	if link != "" {
		result += "\n   View event: " + link
	}
	return result, nil
}

func (h Handler) updateCalendarEvent(r *http.Request, uid string, p map[string]any) (string, error) {
	token, err := h.integrationToken(r.Context(), uid)
	if err != nil {
		return "", err
	}
	id, _ := p["event_id"].(string)
	if strings.TrimSpace(id) == "" {
		var resolveErr error
		id, resolveErr = h.resolveCalendarEventID(r, token, p)
		if resolveErr != nil {
			return "", resolveErr
		}
	}
	endpoint := "https://www.googleapis.com/calendar/v3/calendars/primary/events/" + url.PathEscape(id)
	current, err := h.googleJSON(r, token, endpoint, http.MethodGet, nil)
	if err != nil {
		return "", err
	}
	obj, ok := current.(map[string]any)
	if !ok {
		return "", errors.New("invalid calendar event")
	}
	for key, target := range map[string]string{"title": "summary", "description": "description", "location": "location"} {
		if v, ok := p[key].(string); ok {
			obj[target] = v
		}
	}
	if start, ok := p["start_time"].(string); ok && start != "" {
		t, e := time.Parse(time.RFC3339, start)
		if e != nil {
			return "", e
		}
		obj["start"] = map[string]any{"dateTime": t.Format(time.RFC3339), "timeZone": t.Location().String()}
	}
	if end, ok := p["end_time"].(string); ok && end != "" {
		t, e := time.Parse(time.RFC3339, end)
		if e != nil {
			return "", e
		}
		obj["end"] = map[string]any{"dateTime": t.Format(time.RFC3339), "timeZone": t.Location().String()}
	}
	body, _ := json.Marshal(obj)
	_, err = h.googleJSON(r, token, endpoint, http.MethodPut, body)
	if err != nil {
		return "", err
	}
	title, _ := obj["summary"].(string)
	return "✅ Successfully updated calendar event: " + title, nil
}

func (h Handler) deleteCalendarEvent(r *http.Request, uid string, p map[string]any) (string, error) {
	token, err := h.integrationToken(r.Context(), uid)
	if err != nil {
		return "", err
	}
	id, _ := p["event_id"].(string)
	if strings.TrimSpace(id) == "" {
		var resolveErr error
		id, resolveErr = h.resolveCalendarEventID(r, token, p)
		if resolveErr != nil {
			return "", resolveErr
		}
	}
	endpoint := "https://www.googleapis.com/calendar/v3/calendars/primary/events/" + url.PathEscape(id)
	if _, err := h.googleJSON(r, token, endpoint, http.MethodDelete, nil); err != nil {
		return "", err
	}
	return "✅ Successfully deleted calendar event: " + id, nil
}

func (h Handler) resolveCalendarEventID(r *http.Request, token string, params map[string]any) (string, error) {
	title, _ := params["event_title"].(string)
	if strings.TrimSpace(title) == "" {
		return "", errors.New("event_id or event_title is required")
	}
	value, err := h.calendarEventsValue(r, token, params)
	if err != nil {
		return "", err
	}
	return selectCalendarEventID(value, title)
}

func selectCalendarEventID(value any, title string) (string, error) {
	obj, ok := value.(map[string]any)
	if !ok {
		return "", errors.New("invalid calendar events response")
	}
	items, _ := obj["items"].([]any)
	needle := strings.ToLower(strings.TrimSpace(title))
	var match string
	count := 0
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		summary, _ := item["summary"].(string)
		if !strings.Contains(strings.ToLower(summary), needle) {
			continue
		}
		id, _ := item["id"].(string)
		if id == "" {
			continue
		}
		match = id
		count++
	}
	if count == 0 {
		return "", fmt.Errorf("no calendar events found matching %q", title)
	}
	if count > 1 {
		return "", fmt.Errorf("multiple calendar events found matching %q; provide event_id", title)
	}
	return match, nil
}

func calendarEventBody(p map[string]any, start, end time.Time) []byte {
	event := map[string]any{"summary": p["title"], "start": map[string]any{"dateTime": start.Format(time.RFC3339), "timeZone": start.Location().String()}, "end": map[string]any{"dateTime": end.Format(time.RFC3339), "timeZone": end.Location().String()}}
	for key := range map[string]bool{"description": true, "location": true} {
		if v, ok := p[key].(string); ok && v != "" {
			event[key] = v
		}
	}
	if attendees, ok := p["attendees"].(string); ok && strings.TrimSpace(attendees) != "" {
		list := []map[string]string{}
		for _, email := range strings.Split(attendees, ",") {
			email = strings.TrimSpace(email)
			if email != "" {
				list = append(list, map[string]string{"email": email})
			}
		}
		event["attendees"] = list
	}
	raw, _ := json.Marshal(event)
	return raw
}

func parseGmailMessage(message map[string]any) map[string]any {
	payload, _ := message["payload"].(map[string]any)
	headers := map[string]string{}
	if list, ok := payload["headers"].([]any); ok {
		for _, raw := range list {
			if h, ok := raw.(map[string]any); ok {
				n, _ := h["name"].(string)
				v, _ := h["value"].(string)
				headers[strings.ToLower(n)] = v
			}
		}
	}
	body := gmailBody(payload)
	date := headers["date"]
	if parsed, err := mail.ParseDate(date); err == nil {
		date = parsed.Format(time.RFC3339)
	}
	subject := headers["subject"]
	if subject == "" {
		subject = "(No subject)"
	}
	from := headers["from"]
	if from == "" {
		from = "Unknown"
	}
	to := headers["to"]
	if to == "" {
		to = "Unknown"
	}
	id, _ := message["id"].(string)
	thread, _ := message["threadId"].(string)
	snippet, _ := message["snippet"].(string)
	return map[string]any{"id": id, "threadId": thread, "subject": subject, "from": from, "to": to, "date": date, "snippet": snippet, "body": body}
}

func gmailBody(payload map[string]any) string {
	if mime, _ := payload["mimeType"].(string); mime == "text/plain" || mime == "text/html" {
		if body, ok := payload["body"].(map[string]any); ok {
			if data, ok := body["data"].(string); ok {
				if decoded, err := base64.RawURLEncoding.DecodeString(data); err == nil {
					return string(decoded)
				}
			}
		}
	}
	if parts, ok := payload["parts"].([]any); ok {
		for _, raw := range parts {
			if part, ok := raw.(map[string]any); ok {
				if result := gmailBody(part); result != "" && part["mimeType"] == "text/plain" {
					return result
				}
			}
		}
		for _, raw := range parts {
			if part, ok := raw.(map[string]any); ok {
				if result := gmailBody(part); result != "" {
					return result
				}
			}
		}
	}
	return ""
}

func intParam(values map[string]any, key string, fallback, max int) int {
	v, ok := values[key].(float64)
	if !ok || int(v) < 1 {
		return fallback
	}
	if int(v) > max {
		return max
	}
	return int(v)
}
func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}
