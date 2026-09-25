package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type googleCalendarEvent struct {
	EventID        string    `json:"event_id"`
	Title          string    `json:"title"`
	Attendees      []string  `json:"attendees"`
	AttendeeEmails []string  `json:"attendee_emails"`
	StartTime      time.Time `json:"start_time"`
	EndTime        time.Time `json:"end_time"`
	HTMLLink       string    `json:"html_link,omitempty"`
	Location       string    `json:"location"`
	Description    string    `json:"description"`
	AllDay         bool      `json:"all_day"`
}

type calendarCaptureGap struct {
	EventID   string    `json:"event_id"`
	Title     string    `json:"title"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Status    string    `json:"status"`
	Coverage  string    `json:"coverage"`
}

func parseGoogleCalendarTime(value map[string]any) (time.Time, error) {
	if raw, ok := value["dateTime"].(string); ok && raw != "" {
		return time.Parse(time.RFC3339, raw)
	}
	if raw, ok := value["date"].(string); ok && raw != "" {
		return time.ParseInLocation("2006-01-02", raw, time.UTC)
	}
	return time.Time{}, errors.New("calendar event has no valid time")
}

func convertGoogleEvent(raw map[string]any) (googleCalendarEvent, bool) {
	start, ok1 := raw["start"].(map[string]any)
	end, ok2 := raw["end"].(map[string]any)
	if !ok1 || !ok2 {
		return googleCalendarEvent{}, false
	}
	startAt, e1 := parseGoogleCalendarTime(start)
	endAt, e2 := parseGoogleCalendarTime(end)
	if e1 != nil || e2 != nil || !endAt.After(startAt) {
		return googleCalendarEvent{}, false
	}
	e := googleCalendarEvent{EventID: stringValue(raw["id"]), Title: stringValue(raw["summary"]), StartTime: startAt, EndTime: endAt, HTMLLink: stringValue(raw["htmlLink"]), Location: truncate(stringValue(raw["location"]), 200), Description: truncate(stringValue(raw["description"]), 300), AllDay: start["date"] != nil && start["dateTime"] == nil}
	if e.Title == "" {
		e.Title = "Untitled Event"
	}
	if attendees, ok := raw["attendees"].([]any); ok {
		for _, item := range attendees {
			if a, ok := item.(map[string]any); ok {
				if name := stringValue(a["displayName"]); name != "" {
					e.Attendees = append(e.Attendees, name)
				}
				if email := stringValue(a["email"]); email != "" {
					e.AttendeeEmails = append(e.AttendeeEmails, email)
				}
			}
		}
	}
	return e, true
}

func stringValue(value any) string { s, _ := value.(string); return s }
func truncate(value string, n int) string {
	r := []rune(value)
	if len(r) > n {
		return string(r[:n])
	}
	return value
}

func parseCalendarQueryTime(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	return time.ParseInLocation("2006-01-02T15:04:05", value, time.UTC)
}

func fetchGoogleRaw(ctx context.Context, token string, query url.Values) ([]map[string]any, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/calendar/v3/calendars/primary/events?"+query.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, errors.New("Google Calendar request failed")
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, resp.StatusCode, err
	}
	return payload.Items, resp.StatusCode, nil
}

func (h Handler) CalendarCaptureGaps(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	start, err := parseCalendarQueryTime(r.URL.Query().Get("start"))
	if err != nil {
		http.Error(w, "invalid start", http.StatusBadRequest)
		return
	}
	end, err := parseCalendarQueryTime(r.URL.Query().Get("end"))
	if err != nil || !end.After(start) {
		http.Error(w, "end must be after start", http.StatusBadRequest)
		return
	}
	if end.Sub(start) > 31*24*time.Hour {
		http.Error(w, "window too large (max 31 days)", http.StatusBadRequest)
		return
	}
	integration, err := h.Service.Raw(r.Context(), u, "google_calendar")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	token, _ := integration["access_token"].(string)
	connected, _ := integration["connected"].(bool)
	if !connected || token == "" {
		http.Error(w, "Google Calendar not connected", http.StatusBadRequest)
		return
	}
	query := url.Values{"timeMin": []string{start.UTC().Format(time.RFC3339)}, "timeMax": []string{end.UTC().Format(time.RFC3339)}, "maxResults": []string{"250"}, "singleEvents": []string{"true"}, "orderBy": []string{"startTime"}}
	events, status, err := fetchGoogleRaw(r.Context(), token, query)
	if err != nil {
		if status == http.StatusUnauthorized {
			http.Error(w, "Google Calendar authentication expired. Please reconnect.", 401)
		} else {
			http.Error(w, "Failed to fetch calendar events", 500)
		}
		return
	}
	out := make([]calendarCaptureGap, 0)
	for _, raw := range events {
		event, valid := convertGoogleEvent(raw)
		if !valid || event.AllDay || event.EndTime.Sub(event.StartTime) > 8*time.Hour {
			continue
		}
		calendarStatus := stringValue(raw["status"])
		if calendarStatus == "cancelled" || calendarStatus == "tentative" {
			continue
		}
		if calendarStatus == "" {
			calendarStatus = "confirmed"
		}
		if h.DB == nil {
			http.Error(w, "calendar storage is not configured", 503)
			return
		}
		var overlap int
		err = h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM conversations c JOIN users u ON u.id=c.user_id WHERE u.external_uid=? AND c.started_at < ? AND COALESCE(c.ended_at,c.started_at) > ? AND c.status <> 'failed'`, u, event.EndTime, event.StartTime).Scan(&overlap)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if overlap == 0 {
			out = append(out, calendarCaptureGap{EventID: event.EventID, Title: event.Title, StartTime: event.StartTime, EndTime: event.EndTime, Status: calendarStatus, Coverage: "not_captured"})
		}
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (h Handler) GoogleEvents(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	integration, err := h.Service.Raw(r.Context(), u, "google_calendar")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	token, _ := integration["access_token"].(string)
	connected, _ := integration["connected"].(bool)
	if !connected || strings.TrimSpace(token) == "" {
		http.Error(w, "Google Calendar not connected", http.StatusBadRequest)
		return
	}
	q := url.Values{}
	for _, key := range []string{"timeMin", "timeMax", "q"} {
		if value := r.URL.Query().Get(map[string]string{"timeMin": "timeMin", "timeMax": "timeMax", "q": "q"}[key]); value != "" {
			q.Set(key, value)
		}
	}
	max := 20
	if value, e := strconv.Atoi(r.URL.Query().Get("max_results")); e == nil && value > 0 {
		max = value
	}
	if max > 500 {
		max = 500
	}
	q.Set("maxResults", strconv.Itoa(max))
	q.Set("singleEvents", "true")
	q.Set("orderBy", "startTime")
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://www.googleapis.com/calendar/v3/calendars/primary/events?"+q.Encode(), nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		http.Error(w, "Google Calendar authentication expired. Please reconnect.", http.StatusUnauthorized)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Error(w, "Failed to fetch calendar events", 500)
		return
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if json.NewDecoder(resp.Body).Decode(&payload) != nil {
		http.Error(w, "invalid Google Calendar response", 502)
		return
	}
	out := make([]googleCalendarEvent, 0, len(payload.Items))
	for _, raw := range payload.Items {
		if event, valid := convertGoogleEvent(raw); valid {
			out = append(out, event)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartTime.Before(out[j].StartTime) })
	_ = json.NewEncoder(w).Encode(out)
}
