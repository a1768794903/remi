package actionitems

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"remi/server/internal/auth"
)

type Handler struct{ Service Service }

func (h Handler) Share(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var in struct {
		TaskIDs []string `json:"task_ids"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	token, err := h.Service.Share(r.Context(), uid, in.TaskIDs)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	base := strings.TrimRight(os.Getenv("BASE_API_URL"), "/")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": base + "/tasks/" + token, "token": token})
}

func (h Handler) Shared(w http.ResponseWriter, r *http.Request) {
	share, err := h.Service.ReadShare(r.Context(), r.PathValue("token"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	items, err := h.Service.SharedPreview(r.Context(), share)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tasks := make([]map[string]any, 0, len(items))
	for _, item := range items {
		tasks = append(tasks, map[string]any{"description": item.Description, "due_at": item.DueAt})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"sender_name": share.DisplayName, "tasks": tasks, "count": len(tasks)})
}

func (h Handler) AcceptShare(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Token == "" {
		http.Error(w, "token is required", 400)
		return
	}
	ids, err := h.Service.AcceptShare(r.Context(), uid, in.Token)
	if err != nil {
		if strings.Contains(err.Error(), "already accepted") {
			http.Error(w, err.Error(), 409)
		} else if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "expired") {
			http.NotFound(w, r)
		} else {
			http.Error(w, err.Error(), 400)
		}
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"created": ids, "count": len(ids)})
}

func (h Handler) Search(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	text := r.URL.Query().Get("query")
	if text == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.Service.Search(r.Context(), uid, text, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"action_items": items})
}

func (h Handler) PendingSync(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	pending, synced, err := h.Service.PendingSync(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"pending_export": pending, "synced_items": synced})
}

func (h Handler) IDs(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var completed *bool
	if raw := r.URL.Query().Get("completed"); raw != "" {
		value := raw == "true"
		completed = &value
	}
	ids, err := h.Service.IDs(r.Context(), uid, completed)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ids": ids})
}

func (h Handler) Collection(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		var input CreateInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Description == "" {
			http.Error(w, "description is required", http.StatusBadRequest)
			return
		}
		item, err := h.Service.Create(r.Context(), uid, input)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(item)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	var completed *bool
	if value := r.URL.Query().Get("completed"); value != "" {
		parsed := value == "true"
		completed = &parsed
	}
	items, err := h.Service.List(r.Context(), uid, limit, offset, completed)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"action_items": items, "has_more": limit > 0 && len(items) == limit})
}

func (h Handler) Batch(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPatch {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/v1/action-items/batch-delete" {
		var input struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.IDs) == 0 {
			http.Error(w, "ids are required", http.StatusBadRequest)
			return
		}
		count, err := h.Service.BatchDelete(r.Context(), uid, input.IDs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "Ok", "deleted_count": count})
		return
	}
	if r.Method == http.MethodPatch {
		var syncInputs []BatchSyncInput
		if err := json.NewDecoder(r.Body).Decode(&syncInputs); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		count, err := h.Service.BatchSync(r.Context(), uid, syncInputs)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "updated_count": count})
		return
	}
	var inputs []CreateInput
	if err := json.NewDecoder(r.Body).Decode(&inputs); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	items, err := h.Service.BatchCreate(r.Context(), uid, inputs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"action_items": items, "created_count": len(items)})
}

func (h Handler) Item(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	id := r.PathValue("action_item_id")
	if id == "" {
		http.Error(w, "missing action item id", http.StatusBadRequest)
		return
	}
	item, err := h.Service.Get(r.Context(), uid, id)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(item)
		return
	}
	if r.Method == http.MethodPatch {
		var input UpdateInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		updated, err := h.Service.Update(r.Context(), uid, id, input)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(updated)
		return
	}
	if r.Method == http.MethodDelete {
		if err := h.Service.Delete(r.Context(), uid, id); errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}
