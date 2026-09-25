package developer

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"remi/server/internal/actionitems"
	"remi/server/internal/auth"
	"remi/server/internal/conversations"
	"remi/server/internal/memories"
)

type Handler struct {
	Actions       actionitems.Service
	Memories      memories.Service
	Conversations conversations.Service
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	path := r.URL.Path
	switch {
	case path == "/v1/dev/user/memories" && r.Method == http.MethodGet:
		items, err := h.Memories.List(r.Context(), uid, developerLimit(queryInt(r, "limit"), 25, 100), developerOffset(queryInt(r, "offset")))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list memories")
			return
		}
		writeJSON(w, http.StatusOK, items)
	case path == "/v1/dev/user/memories" && r.Method == http.MethodPost:
		var in memories.CreateInput
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := validateMemoryContent(in.Content); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		item, err := h.Memories.Create(r.Context(), uid, in)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	case path == "/v1/dev/user/memories/batch" && r.Method == http.MethodPost:
		var in struct {
			Memories []memories.CreateInput `json:"memories"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Memories) == 0 || len(in.Memories) > 100 {
			writeError(w, http.StatusBadRequest, "memories must contain between 1 and 100 items")
			return
		}
		for _, memory := range in.Memories {
			if err := validateMemoryContent(memory.Content); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		created := make([]memories.Item, 0, len(in.Memories))
		for _, memory := range in.Memories {
			item, createErr := h.Memories.Create(r.Context(), uid, memory)
			if createErr != nil {
				writeError(w, http.StatusUnprocessableEntity, createErr.Error())
				return
			}
			created = append(created, item)
		}
		writeJSON(w, http.StatusOK, map[string]any{"memories": created, "created_count": len(created)})
	case strings.HasPrefix(path, "/v1/dev/user/memories/") && r.Method == http.MethodGet:
		item, err := h.Memories.Get(r.Context(), uid, strings.TrimPrefix(path, "/v1/dev/user/memories/"))
		if errors.Is(err, memories.ErrNotFound) {
			writeError(w, http.StatusNotFound, "memory not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get memory")
			return
		}
		writeJSON(w, http.StatusOK, item)
	case strings.HasPrefix(path, "/v1/dev/user/memories/") && r.Method == http.MethodPatch:
		id := strings.TrimPrefix(path, "/v1/dev/user/memories/")
		var in memories.UpdateInput
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		item, err := h.Memories.Update(r.Context(), uid, id, in)
		if errors.Is(err, memories.ErrNotFound) {
			writeError(w, http.StatusNotFound, "memory not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	case strings.HasPrefix(path, "/v1/dev/user/memories/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(path, "/v1/dev/user/memories/")
		if err := h.Memories.Delete(r.Context(), uid, id); errors.Is(err, memories.ErrNotFound) {
			writeError(w, http.StatusNotFound, "memory not found")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete memory")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	case path == "/v1/dev/user/action-items" && r.Method == http.MethodGet:
		var completed *bool
		if raw := r.URL.Query().Get("completed"); raw != "" {
			value, parseErr := strconv.ParseBool(raw)
			if parseErr != nil {
				writeError(w, http.StatusBadRequest, "completed must be boolean")
				return
			}
			completed = &value
		}
		items, err := h.Actions.List(r.Context(), uid, developerLimit(queryInt(r, "limit"), 25, 100), developerOffset(queryInt(r, "offset")), completed)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list action items")
			return
		}
		writeJSON(w, http.StatusOK, items)
	case path == "/v1/dev/user/action-items" && r.Method == http.MethodPost:
		var in actionitems.CreateInput
		if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Description) == "" || len([]rune(in.Description)) > 5000 {
			writeError(w, http.StatusBadRequest, "description is required and must be at most 5000 characters")
			return
		}
		if in.Owner == "" {
			in.Owner = "user"
		}
		if in.Status == "" {
			in.Status = "active"
		}
		if in.Source == "" {
			in.Source = "developer_api"
		}
		item, err := h.Actions.Create(r.Context(), uid, in)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	case path == "/v1/dev/user/action-items/batch" && r.Method == http.MethodPost:
		var in struct {
			ActionItems []actionitems.CreateInput `json:"action_items"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.ActionItems) == 0 || len(in.ActionItems) > 100 {
			writeError(w, http.StatusBadRequest, "action_items must contain between 1 and 100 items")
			return
		}
		for i := range in.ActionItems {
			if strings.TrimSpace(in.ActionItems[i].Description) == "" || len([]rune(in.ActionItems[i].Description)) > 5000 {
				writeError(w, http.StatusBadRequest, "invalid action item description")
				return
			}
			if in.ActionItems[i].Owner == "" {
				in.ActionItems[i].Owner = "user"
			}
			if in.ActionItems[i].Status == "" {
				in.ActionItems[i].Status = "active"
			}
			if in.ActionItems[i].Source == "" {
				in.ActionItems[i].Source = "developer_api"
			}
		}
		created, err := h.Actions.BatchCreate(r.Context(), uid, in.ActionItems)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"action_items": created, "created_count": len(created)})
	case strings.HasPrefix(path, "/v1/dev/user/action-items/") && r.Method == http.MethodPatch:
		id := strings.TrimPrefix(path, "/v1/dev/user/action-items/")
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
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	case strings.HasPrefix(path, "/v1/dev/user/action-items/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(path, "/v1/dev/user/action-items/")
		if err := h.Actions.Delete(r.Context(), uid, id); errors.Is(err, actionitems.ErrNotFound) {
			writeError(w, http.StatusNotFound, "action item not found")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete action item")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	case path == "/v1/dev/user/conversations" && r.Method == http.MethodGet:
		items, err := h.Conversations.List(r.Context(), uid, developerLimit(queryInt(r, "limit"), 25, 100), developerOffset(queryInt(r, "offset")))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list conversations")
			return
		}
		writeJSON(w, http.StatusOK, items)
	case path == "/v1/dev/user/conversations" && r.Method == http.MethodPost:
		var in conversations.CreateInput
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		item, err := h.Conversations.Create(r.Context(), uid, in)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, item)
	case strings.HasPrefix(path, "/v1/dev/user/conversations/") && r.Method == http.MethodGet:
		id := strings.TrimPrefix(path, "/v1/dev/user/conversations/")
		item, err := h.Conversations.Get(r.Context(), uid, id)
		if errors.Is(err, conversations.ErrNotFound) {
			writeError(w, http.StatusNotFound, "conversation not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get conversation")
			return
		}
		writeJSON(w, http.StatusOK, item)
	default:
		writeError(w, http.StatusNotFound, "developer endpoint not found")
	}
}

func developerLimit(value, fallback, maximum int) int {
	if value <= 0 {
		return fallback
	}
	if value > maximum {
		return maximum
	}
	return value
}
func developerOffset(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
func queryInt(r *http.Request, key string) int {
	value, _ := strconv.Atoi(r.URL.Query().Get(key))
	return value
}
func validateMemoryContent(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("content is required")
	}
	if len([]rune(value)) > 500 {
		return errors.New("content must be at most 500 characters")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}
