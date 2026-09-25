package chat

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"os"
	"remi/server/internal/auth"
	"strconv"
	"strings"
	"time"
)

type Handler struct{ Service Service }

// Analytics preserves the legacy mobile analytics contract, which sends the
// message id and value as query parameters instead of the newer JSON rating
// endpoint. A value of zero clears the rating.
func (h Handler) Analytics(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("message_id"))
	raw := r.URL.Query().Get("value")
	value, err := strconv.Atoi(raw)
	if id == "" || err != nil || value < -1 || value > 1 {
		http.Error(w, "message_id and value must be provided; value must be -1, 0, or 1", http.StatusBadRequest)
		return
	}
	var rating *int
	if value != 0 {
		rating = &value
	}
	if err = h.Service.Rate(r.Context(), uid, id, rating); errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h Handler) Messages(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		items, err := h.Service.History(r.Context(), uid, limit, offset)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(items)
	case http.MethodPost:
		var in SendInput
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		answer, err := h.Service.Send(r.Context(), uid, in)
		if err != nil {
			if answer.ID != "" {
				payload, _ := json.Marshal(answer)
				w.Header().Set("Content-Type", "text/event-stream")
				encoded := base64.StdEncoding.EncodeToString(payload)
				_, _ = w.Write([]byte("error: provider_unavailable\n\ndone: " + encoded + "\n\n"))
				return
			}
			http.Error(w, err.Error(), 502)
			return
		}
		payload, _ := json.Marshal(answer)
		w.Header().Set("Content-Type", "text/event-stream")
		encoded := base64.StdEncoding.EncodeToString(payload)
		_, _ = w.Write([]byte("done: " + encoded + "\n\n"))
	case http.MethodDelete:
		if err := h.Service.DeleteAll(r.Context(), uid); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func (h Handler) Report(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if err := h.Service.Report(r.Context(), uid, r.PathValue("message_id"), in.Reason); errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "Message reported"})
}
func (h Handler) Rating(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var in struct {
		Rating *int `json:"rating"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if in.Rating != nil && *in.Rating != 1 && *in.Rating != -1 {
		http.Error(w, "invalid rating", 400)
		return
	}
	if err := h.Service.Rate(r.Context(), uid, r.PathValue("message_id"), in.Rating); errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
func (h Handler) Initial(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var in struct {
		AppID     string `json:"app_id"`
		SessionID string `json:"session_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	v, err := h.Service.Initial(r.Context(), uid, in.AppID, in.SessionID)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
func (h Handler) Generate(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var in GenerateInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	v, completionUsage, err := h.Service.GenerateWithUsage(r.Context(), in)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	if h.Service.Usage != nil {
		if detailed, ok := h.Service.Usage.(DetailedUsageRecorder); ok {
			_ = detailed.RecordChatDetailed(r.Context(), uid, completionUsage.InputTokens, completionUsage.OutputTokens, completionUsage.CostMicroUSD)
		} else {
			_ = h.Service.Usage.RecordChat(r.Context(), uid, completionUsage.CostMicroUSD)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"text": v, "app_id": in.AppID})
}

func (h Handler) Completions(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model    string `json:"model"`
		Messages []Turn `json:"messages"`
		Stream   bool   `json:"stream"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	answer, err := h.Service.Provider.Complete(r.Context(), in.Messages)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	id := "chatcmpl-" + uuid.NewString()
	created := time.Now().Unix()
	if in.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		payload := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": in.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": answer}, "finish_reason": nil}}}
		b, _ := json.Marshal(payload)
		_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "object": "chat.completion", "created": created, "model": in.Model, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": answer}, "finish_reason": "stop"}}})
}

func (h Handler) Share(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var in struct {
		MessageIDs []string `json:"message_ids"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.MessageIDs) == 0 {
		http.Error(w, "no message IDs provided", 400)
		return
	}
	secret := os.Getenv("CHAT_SHARE_SECRET")
	token, err := makeShareToken(uid, in.MessageIDs, secret)
	if err != nil {
		http.Error(w, "chat sharing is not configured", 503)
		return
	}
	base := strings.TrimRight(os.Getenv("BASE_API_URL"), "/")
	_ = json.NewEncoder(w).Encode(map[string]string{"url": base + "/chat/" + token, "token": token})
}

func (h Handler) Shared(w http.ResponseWriter, r *http.Request) {
	uid, ids, err := parseShareToken(r.PathValue("token"), os.Getenv("CHAT_SHARE_SECRET"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	items, err := h.Service.Shared(r.Context(), uid, ids)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"sender_name": uid, "messages": items, "count": len(items)})
}
