package voice

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"remi/server/internal/stt"
)

const maxBodyBytes int64 = 200 << 20

type Handler struct{ Provider stt.Provider }

var upgrader = websocket.Upgrader{ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10, CheckOrigin: func(*http.Request) bool { return true }}

func writeError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"detail": detail, "error": detail})
}

func (h Handler) transcribe(w http.ResponseWriter, r *http.Request, audio []byte, codec, language string, sampleRate int) {
	if len(audio) == 0 {
		writeError(w, 400, "No audio data provided")
		return
	}
	if h.Provider == nil {
		writeError(w, 503, "transcription service unavailable")
		return
	}
	result, err := h.Provider.Transcribe(r.Context(), stt.Request{Audio: audio, Codec: codec, Language: language, SampleRate: sampleRate})
	if err != nil {
		writeError(w, 502, "transcription provider unavailable")
		return
	}
	out := map[string]any{"transcript": result.Text, "stt_provider": "configured", "stt_model": "configured", "outcome": "success"}
	if result.Text == "" {
		out["outcome"] = "expected_silence"
	}
	if result.Language != "" {
		out["language"] = result.Language
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h Handler) Transcribe(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes+1)
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/octet-stream") || strings.HasPrefix(contentType, "audio/") {
		audio, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
		if err != nil {
			writeError(w, 400, "unable to read audio")
			return
		}
		if int64(len(audio)) > maxBodyBytes {
			writeError(w, 413, "Body too large")
			return
		}
		sampleRate, err := strconv.Atoi(r.URL.Query().Get("sample_rate"))
		if err != nil || sampleRate == 0 {
			sampleRate = 16000
		}
		channels, err := strconv.Atoi(r.URL.Query().Get("channels"))
		if err != nil || channels == 0 {
			channels = 1
		}
		if sampleRate < 8000 || sampleRate > 48000 {
			writeError(w, 400, "invalid sample_rate")
			return
		}
		if channels < 1 || channels > 2 {
			writeError(w, 400, "invalid channels")
			return
		}
		codec := r.URL.Query().Get("encoding")
		if codec == "" {
			codec = "linear16"
		}
		h.transcribe(w, r, audio, codec, r.URL.Query().Get("language"), sampleRate)
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		if errors.Is(err, http.ErrNotSupported) {
			writeError(w, 400, "multipart form required")
		} else {
			writeError(w, 400, "invalid multipart form")
		}
		return
	}
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		files = r.MultipartForm.File["file"]
	}
	if len(files) == 0 {
		writeError(w, 400, "No files provided")
		return
	}
	var transcripts []string
	language := r.FormValue("language")
	sampleRate, _ := strconv.Atoi(r.FormValue("sample_rate"))
	if sampleRate == 0 {
		sampleRate = 16000
	}
	for _, header := range files {
		file, err := header.Open()
		if err != nil {
			writeError(w, 400, "unable to read audio")
			return
		}
		audio, readErr := io.ReadAll(io.LimitReader(file, maxBodyBytes+1))
		_ = file.Close()
		if readErr != nil || int64(len(audio)) > maxBodyBytes {
			writeError(w, 413, "Body too large")
			return
		}
		result, providerErr := h.Provider.Transcribe(r.Context(), stt.Request{Audio: audio, Codec: "wav", Language: language, SampleRate: sampleRate})
		if providerErr != nil {
			writeError(w, 502, "transcription provider unavailable")
			return
		}
		if strings.TrimSpace(result.Text) != "" {
			transcripts = append(transcripts, strings.TrimSpace(result.Text))
		}
	}
	out := map[string]any{"transcript": strings.Join(transcripts, " "), "stt_provider": "configured", "stt_model": "configured", "outcome": "success"}
	if len(transcripts) == 0 {
		out["outcome"] = "expected_silence"
	}
	if language != "" {
		out["language"] = language
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (h Handler) Stream(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	language := r.URL.Query().Get("language")
	if language == "" {
		language = "en"
	}
	codec := r.URL.Query().Get("codec")
	if codec == "" {
		codec = "linear16"
	}
	sampleRate, err := strconv.Atoi(r.URL.Query().Get("sample_rate"))
	if err != nil || sampleRate == 0 {
		sampleRate = 16000
	}
	channels, err := strconv.Atoi(r.URL.Query().Get("channels"))
	if err != nil || channels == 0 {
		channels = 1
	}
	if codec != "linear16" || sampleRate < 8000 || sampleRate > 48000 || channels != 1 {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid transcription stream parameters"), time.Now().Add(time.Second))
		return
	}
	var audio []byte
	for {
		kind, payload, readErr := conn.ReadMessage()
		if readErr != nil {
			return
		}
		if kind == websocket.TextMessage {
			if strings.TrimSpace(string(payload)) != "finalize" {
				continue
			}
			if h.Provider == nil {
				_ = conn.WriteJSON(map[string]any{"type": "service_status", "status": "stt_failed", "reason": "provider_unavailable"})
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Transcription service unavailable"), time.Now().Add(time.Second))
				return
			}
			result, providerErr := h.Provider.Transcribe(r.Context(), stt.Request{Audio: audio, Codec: codec, Language: language, SampleRate: sampleRate})
			if providerErr != nil {
				_ = conn.WriteJSON(map[string]any{"type": "service_status", "status": "stt_failed", "reason": "provider_error"})
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Transcription service unavailable"), time.Now().Add(time.Second))
				return
			}
			segments := result.Segments
			if len(segments) == 0 && strings.TrimSpace(result.Text) != "" {
				segments = []stt.Segment{{Text: result.Text, StartSec: 0, EndSec: 0}}
			}
			if err := conn.WriteJSON(segments); err != nil {
				return
			}
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		if len(audio)+len(payload) > int(maxBodyBytes) {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "audio payload too large"), time.Now().Add(time.Second))
			return
		}
		audio = append(audio, payload...)
	}
}
