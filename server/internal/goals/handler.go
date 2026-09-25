package goals

import (
	"encoding/json"
	"errors"
	"net/http"
	"remi/server/internal/auth"
	"strconv"
)

type Handler struct{ Service Service }

func (h Handler) uid(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return "", false
	}
	return u, true
}
func (h Handler) Collection(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		v, e := h.Service.List(r.Context(), u, r.URL.Query().Get("include_ended") == "true")
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(v)
		return
	}
	var in map[string]any
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	v, e := h.Service.Create(r.Context(), u, in)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	w.WriteHeader(201)
	_ = json.NewEncoder(w).Encode(v)
}
func (h Handler) Item(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	id := r.PathValue("goal_id")
	if r.Method == http.MethodGet {
		n, e := h.Service.Get(r.Context(), u, id)
		if errors.Is(e, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(response(n, h.Service.latest(r.Context(), n)))
		return
	}
	if r.Method == http.MethodDelete {
		e := h.Service.Delete(r.Context(), u, id)
		if errors.Is(e, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "deleted_id": id})
		return
	}
	var in map[string]any
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	v, e := h.Service.Update(r.Context(), u, id, in)
	if errors.Is(e, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
func (h Handler) Progress(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	v, e := strconv.ParseFloat(r.URL.Query().Get("current_value"), 64)
	if e != nil {
		http.Error(w, "current_value is required", 400)
		return
	}
	out, e := h.Service.Progress(r.Context(), u, r.PathValue("goal_id"), v)
	if errors.Is(e, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_ = json.NewEncoder(w).Encode(out)
}
func (h Handler) Events(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		v, e := h.Service.Events(r.Context(), u, r.PathValue("goal_id"), limit)
		if errors.Is(e, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(v)
		return
	}
	var in map[string]any
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	v, e := h.Service.AppendEvent(r.Context(), u, r.PathValue("goal_id"), in)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func (h Handler) History(w http.ResponseWriter, r *http.Request) {
	h.Events(w, r)
}

func (h Handler) Suggest(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.uid(w, r); !ok {
		return
	}
	http.Error(w, "goal suggestion provider is not configured", http.StatusServiceUnavailable)
}

func (h Handler) ExtractProgress(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.uid(w, r); !ok {
		return
	}
	var input struct {
		Text string `json:"text"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || input.Text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	http.Error(w, "goal progress provider is not configured", http.StatusServiceUnavailable)
}
func (h Handler) Advice(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	if r.PathValue("goal_id") == "" {
		_ = json.NewEncoder(w).Encode(map[string]string{"advice": "Set a goal to get personalized advice!"})
		return
	}
	if _, e := h.Service.Get(r.Context(), u, r.PathValue("goal_id")); e != nil {
		http.NotFound(w, r)
		return
	}
	http.Error(w, "goal advice provider is not configured", 502)
}
func (h Handler) Focus(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	id := r.PathValue("goal_id")
	in := map[string]any{}
	_ = json.NewDecoder(r.Body).Decode(&in)
	in["status"] = "focused"
	if _, ok := in["focus_rank"]; !ok {
		in["focus_rank"] = 0
	}
	v, e := h.Service.Update(r.Context(), u, id, in)
	if errors.Is(e, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
func (h Handler) Unfocus(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	v, e := h.Service.Update(r.Context(), u, r.PathValue("goal_id"), map[string]any{"status": "background"})
	if errors.Is(e, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
func (h Handler) Lifecycle(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if in.Status != "paused" && in.Status != "achieved" && in.Status != "abandoned" {
		http.Error(w, "invalid lifecycle status", 400)
		return
	}
	v, e := h.Service.Update(r.Context(), u, r.PathValue("goal_id"), map[string]any{"status": in.Status})
	if errors.Is(e, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
