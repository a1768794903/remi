package conversations

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/apps"
	"remi/server/internal/auth"
	"remi/server/internal/chat"
	"remi/server/internal/transcripts"
)

type Handler struct {
	Service     Service
	Apps        apps.Service
	Queue       Enqueuer
	Transcripts transcripts.Service
	Provider    chat.Provider
}

func buildFollowupPrompt(words []string) string {
	if len(words) < 10 {
		return ""
	}
	if len(words) > 100 {
		words = words[len(words)-100:]
	}
	return "You will be given the transcript of an in-progress conversation. Suggest the next concise, engaging follow-up question. Output only the question, without markdown.\n\nConversation Transcript:\n" + strings.Join(words, " ")
}

func (h Handler) Followup(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.Provider == nil {
		http.Error(w, "follow-up provider is not configured", http.StatusServiceUnavailable)
		return
	}
	conversationID := r.PathValue("memory_id")
	var item Item
	if conversationID == "0" {
		items, listErr := h.Service.List(r.Context(), uid, 100, 0)
		if listErr != nil {
			http.Error(w, "conversation lookup failed", http.StatusInternalServerError)
			return
		}
		for _, candidate := range items {
			if candidate.Status == "in_progress" {
				item = candidate
				conversationID = candidate.ID
				break
			}
		}
		if conversationID == "0" {
			http.Error(w, "no memory in progress", http.StatusBadRequest)
			return
		}
	} else {
		item, err = h.Service.Get(r.Context(), uid, conversationID)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "conversation lookup failed", http.StatusInternalServerError)
			return
		}
	}
	_ = item
	segments, err := h.Transcripts.List(r.Context(), uid, conversationID)
	if err != nil {
		http.Error(w, "transcript lookup failed", http.StatusInternalServerError)
		return
	}
	words := make([]string, 0, len(segments)*4)
	for _, segment := range segments {
		words = append(words, strings.Fields(segment.Text)...)
	}
	prompt := buildFollowupPrompt(words)
	if prompt == "" {
		writeJSON(w, http.StatusOK, map[string]string{"result": ""})
		return
	}
	answer, err := h.Provider.Complete(r.Context(), []chat.Turn{{Role: "user", Content: prompt}})
	if err != nil {
		http.Error(w, "follow-up generation failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": strings.TrimSpace(answer)})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
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

// RunFinalizationJob is the durable worker dispatch boundary. The API only
// enqueues a validated job; the worker owns the actual LLM/database mutation.
func (h Handler) RunFinalizationJob(w http.ResponseWriter, r *http.Request) {
	if !internalJobAllowed(w, r) {
		return
	}
	var job FinalizationJob
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&job) != nil || strings.TrimSpace(job.UID) == "" || strings.TrimSpace(job.ConversationID) == "" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "dropped", "reason": "invalid_payload"})
		return
	}
	if h.Queue == nil {
		http.Error(w, "finalization queue is not configured", http.StatusServiceUnavailable)
		return
	}
	if err := h.Queue.EnqueueFinalization(r.Context(), job); err != nil {
		http.Error(w, "finalization queue unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued", "conversation_id": job.ConversationID})
}

type MergeEnqueuer interface {
	EnqueueMerge(context.Context, MergeJob) error
}

func (h Handler) Field(w http.ResponseWriter, r *http.Request, field string) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var payload map[string]any
	if json.NewDecoder(r.Body).Decode(&payload) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	value, ok := payload[field].(string)
	if !ok {
		http.Error(w, field+" is required", http.StatusBadRequest)
		return
	}
	in := UpdateInput{}
	if field == "title" {
		in.Title = &value
	} else {
		in.Summary = &value
	}
	item, err := h.Service.Update(r.Context(), uid, r.PathValue("conversation_id"), in)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(item)
}

func (h Handler) Search(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var input struct {
		Query string `json:"query"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || input.Query == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}
	items, err := h.Service.Search(r.Context(), uid, input.Query, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"conversations": items})
}

func (h Handler) Analytics(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	segments, err := h.Transcripts.List(r.Context(), uid, r.PathValue("conversation_id"))
	if errors.Is(err, transcripts.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	type speakerStats struct {
		Speaker     string
		PersonID    *string
		IsUser      bool
		TalkSeconds float64
		WordCount   int
	}
	bySpeaker := map[string]*speakerStats{}
	for _, segment := range segments {
		key := segment.Speaker
		if segment.PersonID != nil {
			key = "person:" + *segment.PersonID
		}
		if segment.IsUser {
			key = "user"
		}
		stats := bySpeaker[key]
		if stats == nil {
			label := segment.Speaker
			if segment.IsUser {
				label = "You"
			}
			if segment.PersonID != nil {
				label = *segment.PersonID
			}
			stats = &speakerStats{Speaker: label, PersonID: segment.PersonID, IsUser: segment.IsUser}
			bySpeaker[key] = stats
		}
		if segment.EndMs > segment.StartMs {
			stats.TalkSeconds += float64(segment.EndMs-segment.StartMs) / 1000
		}
		stats.WordCount += len(strings.Fields(segment.Text))
	}
	totalSeconds := 0.0
	totalWords := 0
	out := make([]map[string]any, 0, len(bySpeaker))
	for _, stats := range bySpeaker {
		totalSeconds += stats.TalkSeconds
		totalWords += stats.WordCount
		wpm := 0.0
		if stats.TalkSeconds > 0 {
			wpm = float64(stats.WordCount) * 60 / stats.TalkSeconds
		}
		out = append(out, map[string]any{"speaker": stats.Speaker, "person_id": stats.PersonID, "is_user": stats.IsUser, "talk_seconds": stats.TalkSeconds, "word_count": stats.WordCount, "words_per_minute": wpm})
	}
	overallWPM := 0.0
	if totalSeconds > 0 {
		overallWPM = float64(totalWords) * 60 / totalSeconds
	}
	for _, item := range out {
		if totalSeconds > 0 {
			item["talk_share"] = item["talk_seconds"].(float64) / totalSeconds
		} else {
			item["talk_share"] = 0.0
		}
	}
	json.NewEncoder(w).Encode(map[string]any{"conversation_id": r.PathValue("conversation_id"), "total_seconds": totalSeconds, "total_words": totalWords, "words_per_minute": overallWPM, "speaker_count": len(out), "speakers": out})
}

func (h Handler) TestPrompt(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var in struct {
		Prompt string `json:"prompt"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Prompt) == "" || len([]rune(in.Prompt)) > 20000 {
		http.Error(w, "prompt is required", 400)
		return
	}
	segments, err := h.Transcripts.List(r.Context(), uid, r.PathValue("conversation_id"))
	if errors.Is(err, transcripts.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	parts := make([]string, 0, len(segments))
	for _, s := range segments {
		if strings.TrimSpace(s.Text) != "" {
			parts = append(parts, s.Text)
		}
	}
	if len(parts) == 0 {
		http.Error(w, "conversation has no text content", 400)
		return
	}
	if h.Provider == nil {
		http.Error(w, "summary provider is not configured", 503)
		return
	}
	answer, err := h.Provider.Complete(r.Context(), []chat.Turn{{Role: "system", Content: "Answer the user's prompt about the following conversation transcript. Return only the answer, without meta-commentary."}, {Role: "user", Content: "Transcript:\n" + strings.Join(parts, "\n") + "\n\nPrompt:\n" + in.Prompt}})
	if err != nil {
		http.Error(w, "summary provider unavailable", 502)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"summary": answer})
}

// Topic returns the same small, write-free projection expected by desktop
// clients. The Python implementation may enrich this with an LLM; the Go
// path keeps the endpoint available even when no provider is configured by
// deriving a deterministic title from the supplied transcript.
func (h Handler) Topic(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.UserID(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var input struct {
		Transcript string `json:"transcript"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || strings.TrimSpace(input.Transcript) == "" || len([]rune(input.Transcript)) > 100000 {
		http.Error(w, "transcript is required", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(input.Transcript, "\r", " "), "\n", " "))
	title := text
	if idx := strings.IndexAny(title, ".!?。！？"); idx > 0 {
		title = title[:idx]
	}
	if runes := []rune(strings.TrimSpace(title)); len(runes) > 80 {
		title = string(runes[:80])
	}
	emoji := "💬"
	lower := strings.ToLower(text)
	if strings.Contains(lower, "meeting") || strings.Contains(lower, "会议") {
		emoji = "📅"
	} else if strings.Contains(lower, "plan") || strings.Contains(lower, "计划") {
		emoji = "📝"
	} else if strings.Contains(lower, "idea") || strings.Contains(lower, "想法") {
		emoji = "💡"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"emoji": emoji, "title": strings.TrimSpace(title)})
}

func (h Handler) FromSegments(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.Service.DB == nil {
		http.Error(w, "conversation storage is not configured", http.StatusServiceUnavailable)
		return
	}
	var in struct {
		TranscriptSegments []struct {
			Text      string  `json:"text"`
			Speaker   string  `json:"speaker"`
			SpeakerID *int    `json:"speaker_id"`
			IsUser    bool    `json:"is_user"`
			PersonID  *string `json:"person_id"`
			Start     float64 `json:"start"`
			End       float64 `json:"end"`
		} `json:"transcript_segments"`
		ClientSessionID string     `json:"client_session_id"`
		StartedAt       *time.Time `json:"started_at"`
		FinishedAt      *time.Time `json:"finished_at"`
		Language        string     `json:"language"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.TranscriptSegments) == 0 || len(in.TranscriptSegments) > 500 {
		http.Error(w, "invalid transcript_segments", 422)
		return
	}
	if in.ClientSessionID != "" {
		var id string
		if e := h.Service.DB.QueryRowContext(r.Context(), `SELECT conversation_id FROM conversation_ingest_sessions WHERE user_external_uid=? AND client_session_id=?`, uid, in.ClientSessionID).Scan(&id); e == nil {
			item, e := h.Service.Get(r.Context(), uid, id)
			if e == nil {
				json.NewEncoder(w).Encode(map[string]any{"id": item.ID, "status": item.Status, "discarded": false, "meeting_treatment_eligible": false})
				return
			}
		}
	}
	started := time.Now().UTC()
	if in.StartedAt != nil {
		started = in.StartedAt.UTC()
	}
	finished := in.FinishedAt
	maxEnd := 0.0
	for _, s := range in.TranscriptSegments {
		if strings.TrimSpace(s.Text) == "" || s.Start < 0 || s.End <= s.Start {
			http.Error(w, "invalid transcript segment", 422)
			return
		}
		if s.End > maxEnd {
			maxEnd = s.End
		}
	}
	if finished == nil {
		t := started.Add(time.Duration(maxEnd * float64(time.Second)))
		finished = &t
	}
	if !finished.After(started) {
		http.Error(w, "finished_at must be after started_at", 422)
		return
	}
	item, err := h.Service.Create(r.Context(), uid, CreateInput{StartedAt: &started})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, s := range in.TranscriptSegments {
		speaker := s.Speaker
		if speaker == "" {
			speaker = "SPEAKER_00"
		}
		sid := 0
		if s.SpeakerID != nil {
			sid = *s.SpeakerID
		}
		_, err = h.Transcripts.Create(r.Context(), uid, item.ID, transcripts.CreateInput{Speaker: speaker, SpeakerID: sid, IsUser: s.IsUser, PersonID: s.PersonID, Text: strings.TrimSpace(s.Text), StartMs: int64(s.Start * 1000), EndMs: int64(s.End * 1000), Source: "on_device"})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	item, err = h.Service.Finalize(r.Context(), uid, item.ID, *finished)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if in.ClientSessionID != "" {
		_, err = h.Service.DB.ExecContext(r.Context(), `INSERT INTO conversation_ingest_sessions(user_external_uid,client_session_id,conversation_id,created_at) VALUES(?,?,?,?)`, uid, in.ClientSessionID, item.ID, time.Now().UTC())
		if err != nil {
			http.Error(w, "conversation creation already in progress", 409)
			return
		}
	}
	json.NewEncoder(w).Encode(map[string]any{"id": item.ID, "status": item.Status, "discarded": false, "meeting_treatment_eligible": false})
}

func (h Handler) Merge(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var input struct {
		ConversationIDs []string `json:"conversation_ids"`
		Reprocess       bool     `json:"reprocess"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.ConversationIDs) < 2 {
		http.Error(w, "at least 2 conversation_ids are required", http.StatusBadRequest)
		return
	}
	item, err := h.Service.Merge(r.Context(), uid, input.ConversationIDs)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if queue, ok := h.Queue.(MergeEnqueuer); ok {
		if err := queue.EnqueueMerge(r.Context(), MergeJob{UID: uid, MergedID: item.ID, SourceIDs: input.ConversationIDs, Reprocess: input.Reprocess}); err != nil {
			http.Error(w, "merge could not be queued", http.StatusServiceUnavailable)
			return
		}
	} else {
		http.Error(w, "merge queue is not configured", http.StatusServiceUnavailable)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"status": "merging", "message": "Merge started", "warning": nil, "conversation_ids": input.ConversationIDs, "merged_conversation": item})
}

func (h Handler) Count(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var statuses []string
	if raw := r.URL.Query().Get("statuses"); raw != "" {
		statuses = strings.Split(raw, ",")
	}
	count, err := h.Service.Count(r.Context(), uid, statuses)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"count": count})
}

func (h Handler) Finalize(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	id := r.PathValue("conversation_id")
	item, err := h.Service.Finalize(r.Context(), uid, id, time.Now().UTC())
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if h.Queue != nil {
		if err := h.Queue.EnqueueFinalization(r.Context(), FinalizationJob{UID: uid, ConversationID: id}); err != nil {
			http.Error(w, "finalization queue unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"conversation": item, "messages": []any{}})
}

func (h Handler) Finalization(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	status, err := h.Service.Finalization(r.Context(), uid, r.PathValue("conversation_id"))
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
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
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		item, err := h.Service.Create(r.Context(), uid, input)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"conversation": item, "messages": []any{}})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, err := h.Service.List(r.Context(), uid, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(items)
}

func (h Handler) Item(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	id := r.PathValue("conversation_id")
	if id == "" {
		http.Error(w, "missing conversation id", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet {
		item, err := h.Service.Get(r.Context(), uid, id)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
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
		item, err := h.Service.Update(r.Context(), uid, id, input)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(item)
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

// Photos returns the conversation-owned photo metadata without exposing the
// parent conversation envelope. This is the legacy mobile/web contract used
// by clients that render conversation photos independently.
func (h Handler) Photos(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	item, err := h.Service.Get(r.Context(), uid, r.PathValue("conversation_id"))
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "conversation photo lookup failed", http.StatusInternalServerError)
		return
	}
	if item.Photos == nil {
		item.Photos = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, item.Photos)
}

// Recording reports whether a conversation has an uploaded recording. The
// Python endpoint intentionally returns a small status object rather than
// exposing storage details; preserve that boundary in Go.
func (h Handler) Recording(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	item, err := h.Service.Get(r.Context(), uid, r.PathValue("conversation_id"))
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "conversation recording lookup failed", http.StatusInternalServerError)
		return
	}
	hasRecording := len(item.AudioFiles) > 0 || len(item.ConversationAudio) > 0
	writeJSON(w, http.StatusOK, map[string]bool{"has_recording": hasRecording})
}

// Events updates the completion state of indexed structured conversation
// events. Python intentionally ignores indexes outside the current event list
// (clients can race with a regenerated summary), but rejects mismatched
// parallel arrays before touching storage.
func (h Handler) Events(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPatch || h.Service.DB == nil {
		if h.Service.DB == nil {
			http.Error(w, "conversation storage is not configured", http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	var input struct {
		EventsIdx []int  `json:"events_idx"`
		Values    []bool `json:"values"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.EventsIdx) != len(input.Values) {
		http.Error(w, "events_idx and values must have the same length", http.StatusUnprocessableEntity)
		return
	}
	conversationID := r.PathValue("conversation_id")
	var raw []byte
	if err := h.Service.DB.QueryRowContext(r.Context(), `SELECT COALESCE(c.structured,JSON_OBJECT()) FROM conversations c JOIN users u ON u.id=c.user_id WHERE c.id=? AND u.external_uid=?`, conversationID, uid).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "conversation event lookup failed", http.StatusInternalServerError)
		}
		return
	}
	var structured map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &structured) != nil || structured == nil {
		structured = map[string]any{}
	}
	events, _ := structured["events"].([]any)
	for i, index := range input.EventsIdx {
		if index < 0 || index >= len(events) {
			continue
		}
		if event, ok := events[index].(map[string]any); ok {
			event["created"] = input.Values[i]
		}
	}
	encoded, err := json.Marshal(structured)
	if err != nil {
		http.Error(w, "failed to encode conversation events", http.StatusInternalServerError)
		return
	}
	if _, err = h.Service.DB.ExecContext(r.Context(), `UPDATE conversations c JOIN users u ON u.id=c.user_id SET c.structured=? WHERE c.id=? AND u.external_uid=?`, encoded, conversationID, uid); err != nil {
		http.Error(w, "failed to update conversation events", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "Ok"})
}

// SuggestedApps projects the app ids selected during conversation
// summarization into the current app catalog. Missing, disabled, or no longer
// visible apps are omitted just like the Python implementation.
func (h Handler) SuggestedApps(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet || h.Service.DB == nil {
		if h.Service.DB == nil {
			http.Error(w, "conversation storage is not configured", http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	conversationID := r.PathValue("conversation_id")
	var raw []byte
	if err := h.Service.DB.QueryRowContext(r.Context(), `SELECT COALESCE(c.structured,JSON_OBJECT()) FROM conversations c JOIN users u ON u.id=c.user_id WHERE c.id=? AND u.external_uid=?`, conversationID, uid).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "conversation lookup failed", http.StatusInternalServerError)
		}
		return
	}
	var structured map[string]any
	if json.Unmarshal(raw, &structured) != nil || structured == nil {
		structured = map[string]any{}
	}
	ids, _ := structured["suggested_apps"].([]any)
	result := make([]apps.App, 0, len(ids))
	for _, rawID := range ids {
		id, ok := rawID.(string)
		if !ok || strings.TrimSpace(id) == "" {
			continue
		}
		item, getErr := h.Apps.Get(r.Context(), uid, id)
		if getErr != nil || item.Disabled || !item.Approved {
			continue
		}
		result = append(result, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggested_apps": result, "conversation_id": conversationID})
}

type calendarEventLink struct {
	EventID       string    `json:"event_id"`
	Title         string    `json:"title"`
	Attendees     []string  `json:"attendees"`
	AttendeeEmail []string  `json:"attendee_emails"`
	StartTime     time.Time `json:"start_time"`
	EndTime       time.Time `json:"end_time"`
	HTMLLink      string    `json:"html_link,omitempty"`
}

func (h Handler) calendarToken(ctx context.Context, uid string) (string, error) {
	var raw []byte
	if err := h.Service.DB.QueryRowContext(ctx, `SELECT COALESCE(u.integrations,JSON_OBJECT()) FROM users u WHERE u.external_uid=?`, uid).Scan(&raw); err != nil {
		return "", err
	}
	var integrations map[string]any
	if json.Unmarshal(raw, &integrations) != nil {
		return "", errors.New("invalid integrations")
	}
	integration, _ := integrations["google_calendar"].(map[string]any)
	connected, _ := integration["connected"].(bool)
	token, _ := integration["access_token"].(string)
	if !connected || strings.TrimSpace(token) == "" {
		return "", errors.New("Google Calendar not connected")
	}
	return token, nil
}

func parseCalendarPoint(raw map[string]any) (time.Time, error) {
	if value, ok := raw["dateTime"].(string); ok && value != "" {
		return time.Parse(time.RFC3339, value)
	}
	if value, ok := raw["date"].(string); ok && value != "" {
		return time.ParseInLocation("2006-01-02", value, time.UTC)
	}
	return time.Time{}, errors.New("calendar event has no valid time")
}

func fetchCalendarEvent(ctx context.Context, token, eventID string) (calendarEventLink, error) {
	endpoint := "https://www.googleapis.com/calendar/v3/calendars/primary/events/" + url.PathEscape(eventID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return calendarEventLink{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return calendarEventLink{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return calendarEventLink{}, errors.New("Google Calendar authentication expired. Please reconnect.")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return calendarEventLink{}, fmt.Errorf("failed to fetch calendar event: %s", resp.Status)
	}
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return calendarEventLink{}, err
	}
	startRaw, _ := raw["start"].(map[string]any)
	endRaw, _ := raw["end"].(map[string]any)
	start, err := parseCalendarPoint(startRaw)
	if err != nil {
		return calendarEventLink{}, err
	}
	end, err := parseCalendarPoint(endRaw)
	if err != nil || !end.After(start) {
		return calendarEventLink{}, errors.New("could not parse calendar event times")
	}
	link := calendarEventLink{EventID: stringValue(raw["id"]), Title: stringValue(raw["summary"]), StartTime: start, EndTime: end, HTMLLink: stringValue(raw["htmlLink"]), Attendees: []string{}, AttendeeEmail: []string{}}
	if link.Title == "" {
		link.Title = "Untitled Event"
	}
	if attendees, ok := raw["attendees"].([]any); ok {
		for _, value := range attendees {
			attendee, _ := value.(map[string]any)
			if name := stringValue(attendee["displayName"]); name != "" {
				link.Attendees = append(link.Attendees, name)
			}
			if email := stringValue(attendee["email"]); email != "" {
				link.AttendeeEmail = append(link.AttendeeEmail, email)
			}
		}
	}
	return link, nil
}

func writeConversationLinkToCalendarEvent(ctx context.Context, token, eventID, conversationID string) {
	baseURL := strings.TrimRight(os.Getenv("BASE_API_URL"), "/")
	if baseURL == "" || strings.TrimSpace(token) == "" || strings.TrimSpace(eventID) == "" || strings.TrimSpace(conversationID) == "" {
		return
	}
	getURL := "https://www.googleapis.com/calendar/v3/calendars/primary/events/" + url.PathEscape(eventID)
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return
	}
	getReq.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 20 * time.Second}
	getResp, err := client.Do(getReq)
	if err != nil {
		return
	}
	defer getResp.Body.Close()
	if getResp.StatusCode < 200 || getResp.StatusCode >= 300 {
		return
	}
	var current map[string]any
	if json.NewDecoder(getResp.Body).Decode(&current) != nil {
		return
	}
	description, _ := current["description"].(string)
	conversationLink := baseURL + "/conversations/" + url.PathEscape(conversationID)
	if strings.Contains(description, conversationLink) {
		return
	}
	if description != "" {
		description += "\n\n"
	}
	description += conversationLink
	payload, _ := json.Marshal(map[string]string{"description": description})
	patchReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, getURL, bytes.NewReader(payload))
	if err != nil {
		return
	}
	patchReq.Header.Set("Authorization", "Bearer "+token)
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := client.Do(patchReq)
	if err == nil {
		_ = patchResp.Body.Close()
	}
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func (h Handler) CalendarEvent(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.Service.DB == nil {
		http.Error(w, "conversation storage is not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("conversation_id")
	var exists int
	if err = h.Service.DB.QueryRowContext(r.Context(), `SELECT 1 FROM conversations c JOIN users u ON u.id=c.user_id WHERE c.id=? AND u.external_uid=?`, id, uid).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, "conversation lookup failed", http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodDelete {
		if _, err = h.Service.DB.ExecContext(r.Context(), `UPDATE conversations c JOIN users u ON u.id=c.user_id SET c.calendar_event=NULL WHERE c.id=? AND u.external_uid=?`, id, uid); err != nil {
			http.Error(w, "failed to unlink calendar event", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "Ok"})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		EventID string `json:"event_id"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || strings.TrimSpace(input.EventID) == "" {
		http.Error(w, "event_id is required", http.StatusBadRequest)
		return
	}
	token, err := h.calendarToken(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	link, err := fetchCalendarEvent(r.Context(), token, input.EventID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "authentication expired") {
			status = http.StatusUnauthorized
		}
		http.Error(w, err.Error(), status)
		return
	}
	encoded, _ := json.Marshal(link)
	if _, err = h.Service.DB.ExecContext(r.Context(), `UPDATE conversations c JOIN users u ON u.id=c.user_id SET c.calendar_event=? WHERE c.id=? AND u.external_uid=?`, encoded, id, uid); err != nil {
		http.Error(w, "failed to link calendar event", http.StatusInternalServerError)
		return
	}
	writeConversationLinkToCalendarEvent(r.Context(), token, link.EventID, id)
	writeJSON(w, http.StatusOK, link)
}

func (h Handler) AutoCalendarEvent(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	id := r.PathValue("conversation_id")
	item, err := h.Service.Get(r.Context(), uid, id)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "conversation lookup failed", http.StatusInternalServerError)
		return
	}
	start := item.StartedAt
	end := start
	if item.EndedAt != nil {
		end = *item.EndedAt
	}
	if start.IsZero() {
		http.Error(w, "Conversation has no timestamp information", http.StatusBadRequest)
		return
	}
	token, err := h.calendarToken(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	query := url.Values{"timeMin": []string{start.UTC().Format(time.RFC3339)}, "timeMax": []string{end.UTC().Format(time.RFC3339)}, "singleEvents": []string{"true"}, "orderBy": []string{"startTime"}, "maxResults": []string{"50"}}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://www.googleapis.com/calendar/v3/calendars/primary/events?"+query.Encode(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		http.Error(w, "failed to fetch calendar events", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		http.Error(w, "Google Calendar authentication expired. Please reconnect.", http.StatusUnauthorized)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Error(w, "failed to fetch calendar events", http.StatusInternalServerError)
		return
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if json.NewDecoder(resp.Body).Decode(&payload) != nil {
		http.Error(w, "invalid Google Calendar response", http.StatusBadGateway)
		return
	}
	selected := ""
	for _, raw := range payload.Items {
		if stringValue(raw["status"]) == "cancelled" || stringValue(raw["id"]) == "" {
			continue
		}
		calendarStart, startErr := parseCalendarPoint(mapValue(raw["start"]))
		calendarEnd, endErr := parseCalendarPoint(mapValue(raw["end"]))
		if startErr == nil && endErr == nil && calendarStart.Before(end) && calendarEnd.After(start) {
			selected = stringValue(raw["id"])
			break
		}
	}
	if selected == "" {
		http.Error(w, "No overlapping calendar event found", http.StatusNotFound)
		return
	}
	link, err := fetchCalendarEvent(r.Context(), token, selected)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	encoded, _ := json.Marshal(link)
	if _, err = h.Service.DB.ExecContext(r.Context(), `UPDATE conversations c JOIN users u ON u.id=c.user_id SET c.calendar_event=? WHERE c.id=? AND u.external_uid=?`, encoded, id, uid); err != nil {
		http.Error(w, "failed to link calendar event", http.StatusInternalServerError)
		return
	}
	writeConversationLinkToCalendarEvent(r.Context(), token, link.EventID, id)
	writeJSON(w, http.StatusOK, link)
}

func mapValue(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}
