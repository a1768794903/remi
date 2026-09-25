package people

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"remi/server/internal/auth"
)

type Service struct{ DB *sql.DB }

func validateName(value string) error {
	value = strings.TrimSpace(value)
	if len([]rune(value)) < 2 || len([]rune(value)) > 40 {
		return errors.New("name must be between 2 and 40 characters")
	}
	return nil
}
func readiness(samples []string, version int, embedding []float64) string {
	if samples == nil {
		return "unknown"
	}
	if len(samples) == 0 {
		return "not_learned"
	}
	if version < 3 {
		return "unknown"
	}
	if len(embedding) == 0 {
		return "saved_sample_awaiting_embedding"
	}
	nonzero := false
	for _, v := range embedding {
		if v != 0 {
			nonzero = true
		}
	}
	if !nonzero {
		return "unknown"
	}
	return "ready"
}
func (s Service) CreateOrGet(ctx context.Context, uid, name string) (map[string]any, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	var id string
	err := s.DB.QueryRowContext(ctx, "SELECT external_id FROM people WHERE user_external_uid = ? AND name = ? LIMIT 1", uid, strings.TrimSpace(name)).Scan(&id)
	if err == nil {
		return s.Get(ctx, uid, id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	id = uuid.NewString()
	now := time.Now().UTC()
	_, err = s.DB.ExecContext(ctx, "INSERT INTO people (external_id,user_external_uid,name,speech_samples,speech_sample_transcripts,speech_samples_version,created_at,updated_at) VALUES (?, ?, ?, JSON_ARRAY(), NULL, 3, ?, ?)", id, uid, strings.TrimSpace(name), now, now)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, uid, id)
}
func (s Service) Get(ctx context.Context, uid, id string) (map[string]any, error) {
	var samplesRaw, transcriptsRaw, embeddingRaw []byte
	var name string
	var created, updated time.Time
	var version int
	err := s.DB.QueryRowContext(ctx, "SELECT name,speech_samples,speech_sample_transcripts,speech_samples_version,speaker_embedding,created_at,updated_at FROM people WHERE user_external_uid = ? AND external_id = ?", uid, id).Scan(&name, &samplesRaw, &transcriptsRaw, &version, &embeddingRaw, &created, &updated)
	if err != nil {
		return nil, err
	}
	var samples []string
	var transcripts []string
	var embedding []float64
	_ = json.Unmarshal(samplesRaw, &samples)
	_ = json.Unmarshal(transcriptsRaw, &transcripts)
	_ = json.Unmarshal(embeddingRaw, &embedding)
	return map[string]any{"id": id, "name": name, "created_at": created, "updated_at": updated, "speech_samples": samples, "speech_sample_transcripts": transcripts, "speech_samples_version": version, "voice_readiness": readiness(samples, version, embedding)}, nil
}
func (s Service) List(ctx context.Context, uid string) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT external_id,name,created_at,updated_at,speech_samples,speech_samples_version FROM people WHERE user_external_uid = ? ORDER BY created_at", uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var created, updated time.Time
		var raw []byte
		var version int
		if err := rows.Scan(&id, &name, &created, &updated, &raw, &version); err != nil {
			return nil, err
		}
		var samples []string
		_ = json.Unmarshal(raw, &samples)
		out = append(out, map[string]any{"id": id, "name": name, "created_at": created, "updated_at": updated, "speech_samples": samples, "speech_samples_version": version, "voice_readiness": readiness(samples, version, nil)})
	}
	return out, rows.Err()
}
func (s Service) Rename(ctx context.Context, uid, id, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	result, err := s.DB.ExecContext(ctx, "UPDATE people SET name = ?, updated_at = UTC_TIMESTAMP(6) WHERE user_external_uid = ? AND external_id = ?", strings.TrimSpace(name), uid, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s Service) Delete(ctx context.Context, uid, id string) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM people WHERE user_external_uid = ? AND external_id = ?", uid, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s Service) DeleteSample(ctx context.Context, uid, id string, index int) error {
	if index < 0 {
		return sql.ErrNoRows
	}
	var raw []byte
	if err := s.DB.QueryRowContext(ctx, "SELECT speech_samples FROM people WHERE user_external_uid = ? AND external_id = ?", uid, id).Scan(&raw); err != nil {
		return err
	}
	var samples []string
	if json.Unmarshal(raw, &samples) != nil || index >= len(samples) {
		return sql.ErrNoRows
	}
	samples = append(samples[:index], samples[index+1:]...)
	encoded, _ := json.Marshal(samples)
	_, err := s.DB.ExecContext(ctx, "UPDATE people SET speech_samples = ?, updated_at = UTC_TIMESTAMP(6) WHERE user_external_uid = ? AND external_id = ?", encoded, uid, id)
	return err
}

type Handler struct{ Service Service }

func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid, ok := user(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	item, err := h.Service.CreateOrGet(r.Context(), uid, in.Name)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, item)
}
func (h Handler) Collection(w http.ResponseWriter, r *http.Request) {
	uid, ok := user(w, r)
	if !ok {
		return
	}
	items, err := h.Service.List(r.Context(), uid)
	if err != nil {
		writeError(w, 500, "failed to list people")
		return
	}
	writeJSON(w, 200, items)
}
func (h Handler) Item(w http.ResponseWriter, r *http.Request) {
	uid, ok := user(w, r)
	if !ok {
		return
	}
	id := r.PathValue("person_id")
	switch r.Method {
	case http.MethodGet:
		item, err := h.Service.Get(r.Context(), uid, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "Person not found")
			return
		}
		if err != nil {
			writeError(w, 500, "failed to get person")
			return
		}
		writeJSON(w, 200, item)
	case http.MethodDelete:
		err := h.Service.Delete(r.Context(), uid, id)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "Person not found")
			return
		}
		if err != nil {
			writeError(w, 500, "failed to delete person")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (h Handler) Rename(w http.ResponseWriter, r *http.Request) {
	uid, ok := user(w, r)
	if !ok {
		return
	}
	if err := h.Service.Rename(r.Context(), uid, r.PathValue("person_id"), r.URL.Query().Get("value")); errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "Person not found")
	} else if err != nil {
		writeError(w, 400, err.Error())
	} else {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	}
}
func (h Handler) DeleteSample(w http.ResponseWriter, r *http.Request) {
	uid, ok := user(w, r)
	if !ok {
		return
	}
	var index int
	_, err := fmt.Sscanf(r.PathValue("sample_index"), "%d", &index)
	if err != nil {
		writeError(w, 404, "Sample not found")
		return
	}
	err = h.Service.DeleteSample(r.Context(), uid, r.PathValue("person_id"), index)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "Sample not found")
		return
	}
	if err != nil {
		writeError(w, 500, "failed to delete sample")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
func user(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := auth.UserID(r.Context())
	if err != nil {
		writeError(w, 401, err.Error())
		return "", false
	}
	return id, true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"detail": message})
}
