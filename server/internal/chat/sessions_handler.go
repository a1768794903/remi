package chat

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"remi/server/internal/auth"
)

type SessionsHandler struct{ Service SessionService }

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func sessionUID(w http.ResponseWriter, r *http.Request) (string, bool) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return "", false
	}
	return uid, true
}

func (h SessionsHandler) Collection(w http.ResponseWriter, r *http.Request) {
	uid, ok := sessionUID(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		var in struct {
			Title *string `json:"title"`
			AppID *string `json:"app_id"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		title, app := "", ""
		if in.Title != nil {
			title = *in.Title
		}
		if in.AppID != nil {
			app = *in.AppID
		}
		v, err := h.Service.Create(r.Context(), uid, title, app)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 201, v)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	var starred *bool
	if raw := r.URL.Query().Get("starred"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			http.Error(w, "invalid starred", 400)
			return
		}
		starred = &v
	}
	v, err := h.Service.List(r.Context(), uid, r.URL.Query().Get("app_id"), limit, offset, starred)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, v)
}
func (h SessionsHandler) Item(w http.ResponseWriter, r *http.Request) {
	uid, ok := sessionUID(w, r)
	if !ok {
		return
	}
	id := r.PathValue("session_id")
	switch r.Method {
	case http.MethodGet:
		v, err := h.Service.Get(r.Context(), uid, id)
		if errors.Is(err, ErrSessionNotFound) {
			http.Error(w, "Chat session not found", 404)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, v)
	case http.MethodPatch:
		var in struct {
			Title   *string `json:"title"`
			Starred *bool   `json:"starred"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, err := h.Service.Update(r.Context(), uid, id, in.Title, in.Starred)
		if errors.Is(err, ErrSessionNotFound) {
			http.Error(w, "Chat session not found", 404)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, v)
	case http.MethodDelete:
		if err := h.Service.Delete(r.Context(), uid, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	default:
		w.WriteHeader(405)
	}
}
func (h SessionsHandler) Count(w http.ResponseWriter, r *http.Request) {
	uid, ok := sessionUID(w, r)
	if !ok {
		return
	}
	n, err := h.Service.Count(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]int{"count": n})
}

func (h SessionsHandler) Initial(w http.ResponseWriter, r *http.Request) {
	uid, ok := sessionUID(w, r)
	if !ok {
		return
	}
	var in struct {
		SessionID string `json:"session_id"`
		AppID     string `json:"app_id"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.SessionID) == "" {
		http.Error(w, "session_id is required", 400)
		return
	}
	v, err := h.Service.Initial(r.Context(), uid, in.AppID, in.SessionID)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, 200, map[string]string{"message": v.Text, "message_id": v.ID})
}

func (h SessionsHandler) Title(w http.ResponseWriter, r *http.Request) {
	uid, ok := sessionUID(w, r)
	if !ok {
		return
	}
	var in struct {
		SessionID string `json:"session_id"`
		Messages  []struct {
			Text   string `json:"text"`
			Sender string `json:"sender"`
		} `json:"messages"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.SessionID) == "" || len(in.Messages) == 0 {
		http.Error(w, "session_id and messages are required", 400)
		return
	}
	var turns []Turn
	for i, m := range in.Messages {
		if i >= 10 {
			break
		}
		role := "user"
		if m.Sender == "ai" {
			role = "assistant"
		}
		turns = append(turns, Turn{Role: role, Content: m.Text})
	}
	answer, err := h.Service.Provider.Complete(r.Context(), turns)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	title := strings.Trim(strings.TrimSpace(answer), "\"'")
	if title == "" {
		title = "New Chat"
	}
	if _, err = h.Service.Update(r.Context(), uid, in.SessionID, &title, nil); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, map[string]string{"title": title})
}

func validateSender(sender string) bool { return sender == "human" || sender == "ai" }
func trimText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("text is required")
	}
	if len(text) > 100000 {
		return "", errors.New("text exceeds maximum length")
	}
	return text, nil
}
