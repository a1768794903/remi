package chat

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

type DesktopHandler struct{ Service Service }

func (h DesktopHandler) Messages(w http.ResponseWriter, r *http.Request) {
	uid, ok := sessionUID(w, r)
	if !ok {
		return
	}
	app, session := r.URL.Query().Get("app_id"), r.URL.Query().Get("session_id")
	switch r.Method {
	case http.MethodPost:
		var in struct {
			Text            string `json:"text"`
			Sender          string `json:"sender"`
			AppID           string `json:"app_id"`
			SessionID       string `json:"session_id"`
			ClientMessageID string `json:"client_message_id"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if in.AppID == "" {
			in.AppID = app
		}
		if in.SessionID == "" {
			in.SessionID = session
		}
		v, err := h.Service.Save(r.Context(), uid, SaveInput{Text: in.Text, Sender: in.Sender, AppID: in.AppID, SessionID: in.SessionID, ClientMessageID: in.ClientMessageID})
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, 200, v)
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		v, err := h.Service.DesktopHistory(r.Context(), uid, app, session, limit, offset)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, v)
	case http.MethodDelete:
		n, err := h.Service.DeleteDesktop(r.Context(), uid, app, session)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, map[string]any{"status": "ok", "deleted_count": n})
	default:
		w.WriteHeader(405)
	}
}
func (h DesktopHandler) Rating(w http.ResponseWriter, r *http.Request) {
	uid, ok := sessionUID(w, r)
	if !ok {
		return
	}
	var in struct {
		Rating *int `json:"rating"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if in.Rating != nil && *in.Rating != 1 && *in.Rating != -1 {
		http.Error(w, "Rating must be 1, -1, or null", 400)
		return
	}
	err := h.Service.Rate(r.Context(), uid, r.PathValue("message_id"), in.Rating)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (h DesktopHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	uid, ok := sessionUID(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, more, err := h.Service.Reconcile(r.Context(), uid, r.URL.Query().Get("app_id"), r.URL.Query().Get("session_id"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, 200, map[string]any{"messages": items, "next_cursor": next, "has_more": more})
}
