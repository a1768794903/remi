package voicechat

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"remi/server/internal/auth"
	"remi/server/internal/chat"
	"remi/server/internal/stt"
)

const maxVoiceChatBody int64 = 200 << 20

type Handler struct {
	Chat     chat.Service
	Provider stt.Provider
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.Provider == nil {
		http.Error(w, "transcription service unavailable", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxVoiceChatBody+1)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "multipart form required", http.StatusBadRequest)
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		files = r.MultipartForm.File["file"]
	}
	if len(files) == 0 {
		http.Error(w, "No files provided", http.StatusBadRequest)
		return
	}
	file, err := files[0].Open()
	if err != nil {
		http.Error(w, "unable to read audio", http.StatusBadRequest)
		return
	}
	audio, readErr := io.ReadAll(io.LimitReader(file, maxVoiceChatBody+1))
	_ = file.Close()
	if readErr != nil || int64(len(audio)) > maxVoiceChatBody {
		http.Error(w, "Body too large", http.StatusRequestEntityTooLarge)
		return
	}
	result, err := h.Provider.Transcribe(r.Context(), stt.Request{Audio: audio, Codec: "wav", Language: r.FormValue("language"), SampleRate: 16000})
	if err != nil {
		writeSSEError(w, "transcription_provider_unavailable")
		return
	}
	if strings.TrimSpace(result.Text) == "" {
		writeSSEError(w, "expected_silence")
		return
	}
	message, err := h.Chat.Send(r.Context(), uid, chat.SendInput{Text: strings.TrimSpace(result.Text), AppID: r.FormValue("app_id"), ChatSessionID: r.FormValue("chat_session_id"), PluginID: r.FormValue("plugin_id")})
	if err != nil {
		if message.ID != "" {
			writeSSEMessage(w, "error: provider_unavailable\n\n"+mustEncodedMessage(message)+"\n\n")
			return
		}
		writeSSEError(w, "chat_provider_unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, mustEncodedMessage(message))
}

func voiceChatFileNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	return []string{names[0]}
}
func doneEvent(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return "done: " + base64.StdEncoding.EncodeToString(raw) + "\n\n", nil
}
func mustEncodedMessage(value any) string {
	event, err := doneEvent(value)
	if err != nil {
		return ""
	}
	return event
}
func writeSSEMessage(w http.ResponseWriter, value string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, value)
}
func writeSSEError(w http.ResponseWriter, reason string) { writeSSEMessage(w, "error: "+reason+"\n\n") }
