package audioplayback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"remi/server/internal/conversations"
)

const (
	SampleRate       = 16000
	PollAfterMillis  = 3000
	maxAudioFileSize = 200_000_000
)

type Service struct {
	Conversations conversations.Service
	Root          string
}

func (s Service) root() string {
	if s.Root != "" {
		return s.Root
	}
	if root := os.Getenv("REMI_AUDIO_DATA_DIR"); root != "" {
		return root
	}
	return filepath.Join("data", "audio")
}

func safePart(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, `/\\`)
}

func (s Service) artifactPath(uid, conversationID, audioID, extension string) (string, error) {
	if !safePart(uid) || !safePart(conversationID) || !safePart(audioID) || (extension != "wav" && extension != "pcm") {
		return "", errors.New("invalid audio path")
	}
	return filepath.Join(s.root(), uid, conversationID, audioID+"."+extension), nil
}

func (s Service) readExisting(uid, conversationID, audioID string) ([]byte, string, error) {
	for _, extension := range []string{"wav", "pcm"} {
		path, err := s.artifactPath(uid, conversationID, audioID, extension)
		if err != nil {
			return nil, "", err
		}
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			if int64(len(data)) > maxAudioFileSize {
				return nil, "", errors.New("audio artifact exceeds size limit")
			}
			return data, extension, nil
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return nil, "", readErr
		}
	}
	return nil, "", os.ErrNotExist
}

func metadataString(file map[string]any, key string) string {
	value, _ := file[key].(string)
	return value
}

func metadataChunks(file map[string]any) []string {
	value, ok := file["chunks"].([]any)
	if !ok {
		if typed, ok := file["chunks"].([]string); ok {
			return typed
		}
		return nil
	}
	result := make([]string, 0, len(value))
	for _, item := range value {
		if name, ok := item.(string); ok {
			result = append(result, name)
		}
	}
	return result
}

func (s Service) localChunkPath(name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", errors.New("invalid audio chunk path")
	}
	clean := filepath.Clean(name)
	if clean == "." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		return "", errors.New("invalid audio chunk path")
	}
	return filepath.Join(s.root(), clean), nil
}

func (s Service) materialize(uid, conversationID string, file map[string]any) ([]byte, string, error) {
	audioID := metadataString(file, "id")
	if audioID == "" {
		return nil, "", errors.New("audio file id is required")
	}
	if data, extension, err := s.readExisting(uid, conversationID, audioID); err == nil {
		return data, extension, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	chunks := metadataChunks(file)
	if len(chunks) == 0 {
		return nil, "", os.ErrNotExist
	}
	var pcm []byte
	for _, chunk := range chunks {
		path, err := s.localChunkPath(chunk)
		if err != nil {
			return nil, "", err
		}
		part, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		if len(pcm)+len(part) > maxAudioFileSize {
			return nil, "", errors.New("audio artifact exceeds size limit")
		}
		pcm = append(pcm, part...)
	}
	wav, err := PCMToWAV(pcm, SampleRate, 1, 2)
	if err != nil {
		return nil, "", err
	}
	path, err := s.artifactPath(uid, conversationID, audioID, "wav")
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(path, wav, 0600); err != nil {
		return nil, "", err
	}
	return wav, "wav", nil
}

func (s Service) file(ctx context.Context, uid, conversationID, audioID string) (map[string]any, error) {
	item, err := s.Conversations.Get(ctx, uid, conversationID)
	if err != nil {
		return nil, conversations.ErrNotFound
	}
	for _, file := range item.AudioFiles {
		if metadataString(file, "id") == audioID {
			return file, nil
		}
	}
	return nil, os.ErrNotExist
}

func (s Service) Precache(ctx context.Context, uid, conversationID string) (map[string]any, error) {
	item, err := s.Conversations.Get(ctx, uid, conversationID)
	if err != nil {
		return nil, conversations.ErrNotFound
	}
	if len(item.AudioFiles) == 0 {
		return map[string]any{"status": "no_audio", "message": "No audio files in conversation"}, nil
	}
	for _, file := range item.AudioFiles {
		_, _, _ = s.materialize(uid, conversationID, file)
	}
	return map[string]any{"status": "started", "audio_file_count": len(item.AudioFiles)}, nil
}

func (s Service) URLs(ctx context.Context, uid, conversationID string) (map[string]any, error) {
	item, err := s.Conversations.Get(ctx, uid, conversationID)
	if err != nil {
		return nil, conversations.ErrNotFound
	}
	result := make([]map[string]any, 0, len(item.AudioFiles))
	pending := false
	for _, file := range item.AudioFiles {
		audioID := metadataString(file, "id")
		if audioID == "" {
			continue
		}
		_, extension, readErr := s.readExisting(uid, conversationID, audioID)
		entry := map[string]any{"id": audioID, "duration": numberValue(file["duration"]), "status": "pending", "signed_url": nil}
		if readErr != nil {
			_, _, _ = s.materialize(uid, conversationID, file)
			_, extension, readErr = s.readExisting(uid, conversationID, audioID)
		}
		if readErr == nil {
			entry["status"] = "cached"
			entry["signed_url"] = fmt.Sprintf("/v1/sync/audio/%s/%s?format=%s", conversationID, audioID, extension)
		} else {
			pending = true
		}
		result = append(result, entry)
	}
	response := map[string]any{"audio_files": result}
	if pending {
		response["poll_after_ms"] = PollAfterMillis
	}
	return response, nil
}

func numberValue(value any) any {
	switch value.(type) {
	case int, int64, float64, float32:
		return value
	default:
		return 0
	}
}

func (s Service) Download(ctx context.Context, w http.ResponseWriter, r *http.Request, uid, conversationID, audioID, format string) {
	if format == "" {
		format = "wav"
	}
	if format != "wav" && format != "pcm" {
		http.Error(w, "format must be wav or pcm", http.StatusBadRequest)
		return
	}
	file, err := s.file(ctx, uid, conversationID, audioID)
	if errors.Is(err, conversations.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		http.Error(w, "audio file not found in conversation", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, extension, err := s.materialize(uid, conversationID, file)
	if errors.Is(err, os.ErrNotExist) {
		_ = jsonResponse(w, http.StatusAccepted, map[string]any{"status": "pending", "poll_after_ms": PollAfterMillis})
		return
	}
	if err != nil {
		http.Error(w, "failed to prepare audio file", http.StatusInternalServerError)
		return
	}
	if format == "wav" && extension == "pcm" {
		data, err = PCMToWAV(data, SampleRate, 1, 2)
		if err != nil {
			http.Error(w, "failed to encode WAV", http.StatusInternalServerError)
			return
		}
	}
	if format == "pcm" && extension == "wav" && len(data) >= 44 {
		data = data[44:]
	}
	contentType := "audio/wav"
	if format == "pcm" {
		contentType = "application/octet-stream"
	}
	totalSize := int64(len(data))
	start, end, partial := ParseRange(r.Header.Get("Range"), totalSize)
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" && !partial {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(data)))
		http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	filename := fmt.Sprintf("conversation_%s_audio_%s.%s", conversationID, audioID, format)
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Type", contentType)
	if partial {
		data = data[start : end+1]
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, totalSize))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
	}
	_, _ = io.Copy(w, bytes.NewReader(data))
}

func jsonResponse(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(value)
}
