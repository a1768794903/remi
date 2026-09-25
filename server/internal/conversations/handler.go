package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/auth"
	"remi/server/internal/chat"
	"remi/server/internal/transcripts"
)

type Handler struct {
	Service     Service
	Queue       Enqueuer
	Transcripts transcripts.Service
	Provider    chat.Provider
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
