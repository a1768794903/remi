package calendarmeetings

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"remi/server/internal/auth"
)

type Handler struct{ Service Service }

func (h Handler) uid(w http.ResponseWriter, r *http.Request) (string, bool) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return "", false
	}
	return uid, true
}
func (h Handler) Collection(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.uid(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		var in Input
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, err := h.Service.Store(r.Context(), uid, in)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"meeting_id": v.ID, "calendar_event_id": v.CalendarEventID, "message": "Meeting stored successfully"})
		return
	}
	start := parseTime(r.URL.Query().Get("start_date"))
	end := parseTime(r.URL.Query().Get("end_date"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	v, err := h.Service.List(r.Context(), uid, start, end, limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
func (h Handler) Item(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.uid(w, r)
	if !ok {
		return
	}
	v, err := h.Service.Get(r.Context(), uid, r.PathValue("meeting_id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
func parseTime(v string) *time.Time {
	if v == "" {
		return nil
	}
	t, e := time.Parse(time.RFC3339, v)
	if e != nil {
		return nil
	}
	return &t
}
