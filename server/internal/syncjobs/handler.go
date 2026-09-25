package syncjobs

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/redis/go-redis/v9"
	"io"
	"net/http"
	"os"
	"remi/server/internal/audioplayback"
	"remi/server/internal/auth"
	"remi/server/internal/capturemanifest"
	"remi/server/internal/conversations"
	"strings"
	"time"
)

type Handler struct {
	Queue         Queue
	Conversations conversations.Service
	Audio         audioplayback.Service
}

func internalJobAllowed(w http.ResponseWriter, r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("INTERNAL_JOB_SECRET"))
	provided := strings.TrimSpace(r.Header.Get("X-Internal-Job-Key"))
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		http.Error(w, "invalid internal job credentials", http.StatusForbidden)
		return false
	}
	return true
}

// Run accepts an already-created Go sync job and places it on the same queue
// consumed by cmd/worker. It never acknowledges a job as completed.
func (h Handler) Run(w http.ResponseWriter, r *http.Request) {
	if !internalJobAllowed(w, r) {
		return
	}
	var job Job
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&job) != nil || strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.UID) == "" {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "dropped", "reason": "invalid_payload"})
		return
	}
	if h.Queue.Client == nil {
		http.Error(w, "sync queue unavailable", 503)
		return
	}
	if err := h.Queue.Save(r.Context(), job); err != nil {
		http.Error(w, "sync job persistence unavailable", 503)
		return
	}
	if err := h.Queue.Enqueue(r.Context(), job); err != nil {
		http.Error(w, "sync queue unavailable", 503)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "queued", "job_id": job.ID})
}

func (h Handler) PrecacheAudio(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	value, err := h.Audio.Precache(r.Context(), uid, r.PathValue("conversation_id"))
	if errors.Is(err, conversations.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func (h Handler) AudioURLs(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	value, err := h.Audio.URLs(r.Context(), uid, r.PathValue("conversation_id"))
	if errors.Is(err, conversations.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func (h Handler) DownloadAudio(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	h.Audio.Download(r.Context(), w, r, uid, r.PathValue("conversation_id"), r.PathValue("audio_file_id"), r.URL.Query().Get("format"))
}

type manifestRequest struct {
	ConversationID string                  `json:"conversation_id"`
	Files          []capturemanifest.Claim `json:"files"`
}

func (h Handler) Manifest(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var request manifestRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&request) != nil || request.ConversationID == "" || len(request.Files) == 0 {
		http.Error(w, "invalid capture manifest", http.StatusBadRequest)
		return
	}
	if _, err = h.Conversations.Get(r.Context(), uid, request.ConversationID); err != nil {
		http.Error(w, "conversation not found", http.StatusForbidden)
		return
	}
	device := strings.TrimSpace(r.Header.Get("X-Device-Id-Hash"))
	if device == "" {
		http.Error(w, "device identity is required", http.StatusForbidden)
		return
	}
	manifest, err := capturemanifest.Issue(uid, device, request.ConversationID, request.Files, time.Now().UTC())
	if err != nil {
		http.Error(w, "capture manifest unavailable", http.StatusServiceUnavailable)
		return
	}
	if h.Queue.Client != nil {
		fingerprint, fingerprintErr := capturemanifest.Fingerprint(request.Files)
		if fingerprintErr != nil {
			http.Error(w, "invalid capture manifest", http.StatusBadRequest)
			return
		}
		key := "remi:sync-capture-manifest:" + uid + ":" + request.ConversationID
		claimed, claimErr := h.Queue.Client.SetNX(r.Context(), key, fingerprint, 6*time.Hour).Result()
		if claimErr != nil {
			http.Error(w, "capture manifest unavailable", http.StatusServiceUnavailable)
			return
		}
		if !claimed {
			existing, getErr := h.Queue.Client.Get(r.Context(), key).Result()
			if getErr != nil || existing != fingerprint {
				http.Error(w, "conversation fresh content was already claimed", http.StatusConflict)
				return
			}
		}
	} else {
		http.Error(w, "capture manifest unavailable", http.StatusServiceUnavailable)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"manifest": manifest})
}

func (h Handler) Start(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes+1)
	if e = r.ParseMultipartForm(8 << 20); e != nil {
		http.Error(w, "invalid multipart form", 400)
		return
	}
	conversationID := r.URL.Query().Get("conversation_id")
	if conversationID != "" {
		if _, e = h.Conversations.Get(r.Context(), uid, conversationID); e != nil {
			http.Error(w, "conversation not found", 404)
			return
		}
	}
	parts := make([]multipartPart, 0)
	for _, headers := range r.MultipartForm.File {
		for _, header := range headers {
			if header.Size > MaxUploadBytes {
				http.Error(w, "body too large", 413)
				return
			}
			f, e := header.Open()
			if e != nil {
				http.Error(w, "unable to read audio", 400)
				return
			}
			data, e := io.ReadAll(io.LimitReader(f, MaxUploadBytes+1))
			_ = f.Close()
			if e != nil || int64(len(data)) > MaxUploadBytes {
				http.Error(w, "body too large", 413)
				return
			}
			parts = append(parts, multipartPart{Name: header.Filename, Data: data})
		}
	}
	if len(parts) == 0 {
		http.Error(w, "audio files are required", 400)
		return
	}
	if token := r.Header.Get("X-Omi-Sync-Capture-Manifest"); token != "" {
		device := strings.TrimSpace(r.Header.Get("X-Device-Id-Hash"))
		names := make([]string, 0, len(parts))
		files := make(map[string][]byte, len(parts))
		for _, part := range parts {
			names = append(names, part.Name)
			files[part.Name] = part.Data
		}
		claims, valid := capturemanifest.Verify(token, uid, device, conversationID, names, time.Now().UTC())
		if !valid || !capturemanifest.ClaimsMatchBytes(claims, files) {
			http.Error(w, "capture manifest does not match uploaded audio", http.StatusForbidden)
			return
		}
	}
	id := NewJob(uid, conversationID, nil)
	files, e := h.Queue.WriteFiles(id.ID, parts)
	if e != nil {
		http.Error(w, "could not persist sync files", 500)
		return
	}
	id.Files = files
	if e = h.Queue.Enqueue(r.Context(), id); e != nil {
		h.Queue.RemoveFiles(id.ID)
		http.Error(w, "sync queue unavailable", 503)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"job_id": id.ID, "status": "queued", "total_segments": 0, "processed_segments": 0, "successful_segments": 0, "failed_segments": 0, "lane": "fresh"})
}
func (h Handler) Status(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	j, e := h.Queue.Get(r.Context(), r.PathValue("job_id"))
	if errors.Is(e, redis.Nil) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	if j.UID != uid {
		http.Error(w, "not authorized to view this sync job", 403)
		return
	}
	_ = json.NewEncoder(w).Encode(j)
}
