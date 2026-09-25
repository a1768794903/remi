package tts

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	maxTextLength = 4096
	model         = "gpt-4o-mini-tts"
)

var allowedVoices = map[string]struct{}{
	"alloy": {}, "ash": {}, "ballad": {}, "coral": {}, "echo": {}, "fable": {}, "nova": {},
	"onyx": {}, "sage": {}, "shimmer": {}, "verse": {}, "marin": {}, "cedar": {},
}

type Handler struct {
	APIKey   string
	Endpoint string
	Client   *http.Client
}

type request struct {
	Text         string `json:"text"`
	VoiceID      string `json:"voice_id"`
	Instructions string `json:"instructions,omitempty"`
}

func (h Handler) Synthesize(w http.ResponseWriter, r *http.Request) {
	var input request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTextLength*4)).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	input.Text = strings.TrimSpace(input.Text)
	input.VoiceID = strings.TrimSpace(input.VoiceID)
	if input.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	if len([]rune(input.Text)) > maxTextLength {
		writeError(w, http.StatusBadRequest, "text is too long")
		return
	}
	if _, ok := allowedVoices[input.VoiceID]; !ok {
		writeError(w, http.StatusBadRequest, "voice_id is not supported")
		return
	}
	if strings.TrimSpace(h.APIKey) == "" || strings.TrimSpace(h.Endpoint) == "" {
		writeError(w, http.StatusServiceUnavailable, "OpenAI TTS is not configured")
		return
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	payload := map[string]string{"model": model, "input": input.Text, "voice": input.VoiceID, "response_format": "mp3"}
	if strings.TrimSpace(input.Instructions) != "" {
		payload["instructions"] = strings.TrimSpace(input.Instructions)
	}
	body, _ := json.Marshal(payload)
	var response *http.Response
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		req, requestErr := http.NewRequestWithContext(r.Context(), http.MethodPost, h.Endpoint, bytes.NewReader(body))
		if requestErr != nil {
			writeError(w, http.StatusBadGateway, "OpenAI TTS request failed")
			return
		}
		req.Header.Set("Authorization", "Bearer "+h.APIKey)
		req.Header.Set("Content-Type", "application/json")
		response, err = client.Do(req)
		if err != nil {
			if attempt == 2 {
				writeError(w, http.StatusBadGateway, "OpenAI TTS request failed")
				return
			}
			continue
		}
		if !retryable(response.StatusCode) || attempt == 2 {
			break
		}
		response.Body.Close()
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		io.Copy(io.Discard, response.Body)
		writeError(w, response.StatusCode, "OpenAI TTS request failed")
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, response.Body)
}

func retryable(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests, 500, 502, 503, 504, 529:
		return true
	default:
		return false
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": message})
}
