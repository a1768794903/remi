package transcripts

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"remi/server/internal/auth"
)

type Handler struct{ Service Service }

func (h Handler) AssignSegment(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	index, err := strconv.Atoi(r.PathValue("segment_idx"))
	if err != nil {
		http.Error(w, "invalid segment index", http.StatusBadRequest)
		return
	}
	err = h.Service.AssignSegment(r.Context(), uid, r.PathValue("conversation_id"), index, r.URL.Query().Get("assign_type"), r.URL.Query().Get("value"))
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "Ok"})
}

func (h Handler) AssignSpeaker(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	speakerID, err := strconv.Atoi(r.PathValue("speaker_id"))
	if err != nil {
		http.Error(w, "invalid speaker id", http.StatusBadRequest)
		return
	}
	err = h.Service.AssignSpeaker(r.Context(), uid, r.PathValue("conversation_id"), speakerID, r.URL.Query().Get("assign_type"), r.URL.Query().Get("value"))
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "Ok"})
}

func (h Handler) AssignBulk(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var input struct {
		SegmentIDs []string `json:"segment_ids"`
		AssignType string   `json:"assign_type"`
		Value      string   `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || len(input.SegmentIDs) == 0 {
		http.Error(w, "segment_ids are required", http.StatusBadRequest)
		return
	}
	err = h.Service.AssignBulk(r.Context(), uid, r.PathValue("conversation_id"), input.SegmentIDs, input.AssignType, input.Value)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "Ok"})
}

func (h Handler) UpdateText(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var input struct {
		SegmentID string `json:"segment_id"`
		Text      string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.SegmentID == "" || input.Text == "" {
		http.Error(w, "segment_id and text are required", http.StatusBadRequest)
		return
	}
	err = h.Service.UpdateText(r.Context(), uid, r.PathValue("conversation_id"), input.SegmentID, input.Text)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "Ok"})
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	id := r.PathValue("conversation_id")
	segments, err := h.Service.List(r.Context(), uid, id)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"stt": segments})
}

func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var input CreateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	segment, err := h.Service.Create(r.Context(), uid, r.PathValue("conversation_id"), input)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(segment)
}
