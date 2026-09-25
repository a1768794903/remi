package stt

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

const MaxUploadBytes int64 = 200_000_000

type Handler struct{ Provider Provider }

func (h Handler) Transcribe(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes+1)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		if errors.Is(err, http.ErrNotSupported) {
			http.Error(w, "multipart form required", http.StatusBadRequest)
			return
		}
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "audio file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	if header.Size > MaxUploadBytes {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	audio, err := io.ReadAll(io.LimitReader(file, MaxUploadBytes+1))
	if err != nil {
		http.Error(w, "unable to read audio", http.StatusBadRequest)
		return
	}
	if len(audio) == 0 {
		http.Error(w, "no audio data provided", http.StatusBadRequest)
		return
	}
	if int64(len(audio)) > MaxUploadBytes {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	sampleRate, _ := strconv.Atoi(r.FormValue("sample_rate"))
	diarize := true
	if value := r.FormValue("diarize"); value != "" {
		diarize = value != "false" && value != "0"
	}
	result, err := h.Provider.Transcribe(r.Context(), Request{Audio: audio, Codec: r.FormValue("codec"), Language: r.FormValue("language"), SampleRate: sampleRate, Filename: header.Filename, ContentType: header.Header.Get("Content-Type"), Diarize: diarize})
	if err != nil {
		var upstream *UpstreamError
		if errors.As(err, &upstream) && (upstream.Status == http.StatusRequestEntityTooLarge || upstream.Status == http.StatusServiceUnavailable) {
			http.Error(w, upstream.Message, upstream.Status)
			return
		}
		http.Error(w, "transcription upstream unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
