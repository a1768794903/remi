package folders

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"remi/server/ent"
	"remi/server/ent/conversation"
	"remi/server/internal/auth"
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
		v, e := h.Service.List(r.Context(), u)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(v)
		return
	}
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Color       string `json:"color"`
		Icon        string `json:"icon"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	v, e := h.Service.Create(r.Context(), u, in.Name, in.Description, in.Color, in.Icon)
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
	id := r.PathValue("folder_id")
	n, e := h.Service.Get(r.Context(), u, id)
	if errors.Is(e, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	switch r.Method {
	case http.MethodGet:
		c, _ := n.QueryConversations().Count(r.Context())
		_ = json.NewEncoder(w).Encode(toItem(n, c))
	case http.MethodPatch:
		var in map[string]any
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, e := h.Service.Update(r.Context(), u, id, in)
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	case http.MethodDelete:
		if e := h.Service.Delete(r.Context(), u, id); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}
func (h Handler) Conversations(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	n, e := h.Service.Get(r.Context(), u, r.PathValue("folder_id"))
	if errors.Is(e, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	ns, e := n.QueryConversations().Order(ent.Desc(conversation.FieldStartedAt)).Limit(limit).Offset(offset).All(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	out := make([]map[string]any, 0, len(ns))
	for _, c := range ns {
		out = append(out, map[string]any{"id": strconv.Itoa(c.ID), "title": c.Title, "summary": c.Summary, "visibility": c.Visibility, "starred": c.Starred, "status": string(c.Status), "started_at": c.StartedAt, "ended_at": c.EndedAt, "created_at": c.CreatedAt, "updated_at": c.UpdatedAt})
	}
	_ = json.NewEncoder(w).Encode(out)
}
func (h Handler) Move(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	var in struct {
		FolderID *string `json:"folder_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	id := ""
	if in.FolderID != nil {
		id = *in.FolderID
	}
	if e := h.Service.Move(r.Context(), u, r.PathValue("conversation_id"), id); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
func (h Handler) Bulk(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	var in struct {
		ConversationIDs []string `json:"conversation_ids"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	n := 0
	for _, id := range in.ConversationIDs {
		if e := h.Service.Move(r.Context(), u, id, r.PathValue("folder_id")); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		n++
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "moved_count": n})
}
func (h Handler) Reorder(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	var in struct {
		FolderIDs []string `json:"folder_ids"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	for i, id := range in.FolderIDs {
		n, e := h.Service.Get(r.Context(), u, id)
		if e != nil {
			http.Error(w, "unknown folder ID", 422)
			return
		}
		_, e = h.Service.Client.Folder.UpdateOneID(n.ID).SetOrder(i).Save(r.Context())
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
