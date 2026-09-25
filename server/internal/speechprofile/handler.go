package speechprofile

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"remi/server/internal/auth"
)

type Handler struct {
	DB           *sql.DB
	Directory    string
	STTAvailable func() bool
	Embed        func(context.Context, []byte) ([]float64, error)
}

func (h Handler) uid(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return "", false
	}
	if h.DB == nil {
		http.Error(w, "speech profile storage is not configured", 503)
		return "", false
	}
	return u, true
}
func jsonOut(w http.ResponseWriter, s int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s)
	_ = json.NewEncoder(w).Encode(v)
}
func (h Handler) dir() string {
	if h.Directory != "" {
		return h.Directory
	}
	if d := os.Getenv("SPEECH_PROFILE_DIR"); d != "" {
		return d
	}
	return filepath.Join("data", "speech-profiles")
}
func (h Handler) Profile(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	var url sql.NullString
	err := h.DB.QueryRowContext(r.Context(), `SELECT profile_url FROM speech_profiles WHERE user_external_uid=?`, u).Scan(&url)
	has := err == nil && url.Valid && url.String != ""
	jsonOut(w, 200, map[string]any{"has_profile": has})
}
func (h Handler) Availability(w http.ResponseWriter, r *http.Request) {
	available := false
	if h.STTAvailable != nil {
		available = h.STTAvailable()
	} else {
		available = os.Getenv("HOSTED_PARAKEET_API_URL") != "" || os.Getenv("MODULATE_API_KEY") != "" || os.Getenv("DEEPGRAM_API_KEY") != ""
	}
	jsonOut(w, 200, map[string]bool{"available": available})
}
func (h Handler) Audio(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	var url sql.NullString
	err := h.DB.QueryRowContext(r.Context(), `SELECT profile_url FROM speech_profiles WHERE user_external_uid=?`, u).Scan(&url)
	if err != nil || !url.Valid {
		jsonOut(w, 200, map[string]any{"url": nil})
		return
	}
	jsonOut(w, 200, map[string]string{"url": url.String})
}
func (h Handler) Status(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	var url sql.NullString
	var duration float64
	var samples []byte
	err := h.DB.QueryRowContext(r.Context(), `SELECT profile_url,duration_seconds,COALESCE(extra_samples,JSON_ARRAY()) FROM speech_profiles WHERE user_external_uid=?`, u).Scan(&url, &duration, &samples)
	if err != nil {
		jsonOut(w, 200, map[string]any{"has_profile": false, "duration_seconds": 0, "sample_count": 0, "url": nil})
		return
	}
	var list []any
	_ = json.Unmarshal(samples, &list)
	jsonOut(w, 200, map[string]any{"has_profile": url.Valid && url.String != "", "duration_seconds": duration, "sample_count": len(list), "url": url.String})
}
func (h Handler) Expand(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		var raw []byte
		err := h.DB.QueryRowContext(r.Context(), `SELECT COALESCE(extra_samples,JSON_ARRAY()) FROM speech_profiles WHERE user_external_uid=?`, u).Scan(&raw)
		if err != nil {
			jsonOut(w, 200, []string{})
			return
		}
		var list []string
		_ = json.Unmarshal(raw, &list)
		jsonOut(w, 200, list)
		return
	}
	name := r.URL.Query().Get("file_name")
	if name == "" {
		name = r.URL.Query().Get("memory_id") + "_segment_" + r.URL.Query().Get("segment_idx") + ".wav"
	}
	name = filepath.Base(name)
	if name == "." || name == "" {
		http.Error(w, "file_name is required", 400)
		return
	}
	var raw []byte
	_ = h.DB.QueryRowContext(r.Context(), `SELECT COALESCE(extra_samples,JSON_ARRAY()) FROM speech_profiles WHERE user_external_uid=?`, u).Scan(&raw)
	var list []string
	_ = json.Unmarshal(raw, &list)
	filtered := list[:0]
	for _, x := range list {
		if x != name {
			filtered = append(filtered, x)
		}
	}
	next, _ := json.Marshal(filtered)
	_, err := h.DB.ExecContext(r.Context(), `UPDATE speech_profiles SET extra_samples=?,updated_at=UTC_TIMESTAMP(6) WHERE user_external_uid=?`, next, u)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = os.Remove(filepath.Join(h.dir(), u, name))
	jsonOut(w, 200, map[string]string{"status": "ok"})
}

func parseWAV(r io.Reader) (int, float64, []byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 25*1024*1024))
	if err != nil {
		return 0, 0, nil, err
	}
	if len(raw) < 44 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return 0, 0, nil, errors.New("invalid WAV")
	}
	rate := int(binary.LittleEndian.Uint32(raw[24:28]))
	channels := int(binary.LittleEndian.Uint16(raw[22:24]))
	bits := int(binary.LittleEndian.Uint16(raw[34:36]))
	if rate != 16000 || channels < 1 || bits < 8 {
		return 0, 0, nil, errors.New("WAV must be 16kHz")
	}
	byteRate := int(binary.LittleEndian.Uint32(raw[28:32]))
	if byteRate < 1 {
		return 0, 0, nil, errors.New("invalid WAV byte rate")
	}
	duration := float64(len(raw)-44) / float64(byteRate)
	return rate, duration, raw, nil
}
func filePart(r *http.Request) (*multipart.FileHeader, error) {
	if err := r.ParseMultipartForm(25 * 1024 * 1024); err != nil {
		return nil, err
	}
	_, hdr, err := r.FormFile("file")
	return hdr, err
}
func (h Handler) Upload(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	part, err := filePart(r)
	if err != nil {
		http.Error(w, "file is required", 400)
		return
	}
	f, err := part.Open()
	if err != nil {
		http.Error(w, "invalid upload", 400)
		return
	}
	defer f.Close()
	_, duration, raw, err := parseWAV(f)
	if err != nil || duration < 5 || duration > 180 {
		http.Error(w, "Audio duration is invalid (must be 5-180 seconds)", 400)
		return
	}
	if h.Embed == nil {
		http.Error(w, "speaker embedding provider is not configured", http.StatusServiceUnavailable)
		return
	}
	embedding, err := h.Embed(r.Context(), raw)
	if err != nil || len(embedding) == 0 {
		http.Error(w, "failed to extract speaker embedding", http.StatusServiceUnavailable)
		return
	}
	if err := os.MkdirAll(filepath.Join(h.dir(), u), 0700); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	path := filepath.Join(h.dir(), u, "profile.wav")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	url := "/v4/speech-profile/audio"
	embeddingRaw, _ := json.Marshal(embedding)
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO speech_profiles(user_external_uid,profile_url,duration_seconds,embedding,extra_samples,created_at,updated_at) VALUES(?,?,?, ?,JSON_ARRAY(),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE profile_url=VALUES(profile_url),duration_seconds=VALUES(duration_seconds),embedding=VALUES(embedding),updated_at=VALUES(updated_at)`, u, url, duration, embeddingRaw)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonOut(w, 200, map[string]string{"url": url})
}

func HTTPEmbedder(endpoint, apiKey string) func(context.Context, []byte) ([]float64, error) {
	return func(ctx context.Context, audio []byte) ([]float64, error) {
		if endpoint == "" {
			return nil, errors.New("embedding endpoint is not configured")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(audio))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "audio/wav")
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("embedding provider returned %s", resp.Status)
		}
		var payload struct {
			Embedding []float64 `json:"embedding"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
			return nil, err
		}
		return payload.Embedding, nil
	}
}
func (h Handler) ServeAudio(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	path := filepath.Join(h.dir(), u, "profile.wav")
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	http.ServeFile(w, r, path)
}
