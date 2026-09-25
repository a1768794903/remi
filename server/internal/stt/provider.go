package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Request struct {
	Audio       []byte
	SampleRate  int
	Codec       string
	Language    string
	Filename    string
	ContentType string
	Diarize     bool
}

type Segment struct {
	Text       string  `json:"text"`
	Speaker    string  `json:"speaker,omitempty"`
	StartSec   float64 `json:"start"`
	EndSec     float64 `json:"end"`
	Confidence float64 `json:"confidence,omitempty"`
}

type Result struct {
	Text             string    `json:"text"`
	Language         string    `json:"language,omitempty"`
	DetectedLanguage string    `json:"detected_language,omitempty"`
	Segments         []Segment `json:"segments"`
}

type Provider interface {
	Transcribe(context.Context, Request) (Result, error)
}

type UpstreamError struct {
	Status  int
	Message string
}

func (e *UpstreamError) Error() string { return e.Message }

type NullProvider struct{}

func (NullProvider) Transcribe(_ context.Context, _ Request) (Result, error) {
	return Result{}, nil
}

type HTTPProvider struct {
	Endpoint string
	APIKey   string
	Client   *http.Client
}

func (p HTTPProvider) Transcribe(ctx context.Context, request Request) (Result, error) {
	if p.Endpoint == "" {
		return Result{}, errors.New("stt endpoint is not configured")
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filename := filepath.Base(strings.TrimSpace(request.Filename))
	if filename == "." || filename == "" || filename == ".." {
		filename = "audio.wav"
	}
	filename = sanitizeFilename(filename)
	contentType := request.ContentType
	if contentType == "" {
		contentType = contentTypeForCodec(request.Codec)
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return Result{}, err
	}
	if _, err = part.Write(request.Audio); err != nil {
		return Result{}, err
	}
	if err = writer.WriteField("diarize", strconv.FormatBool(request.Diarize)); err != nil {
		return Result{}, err
	}
	if err = writer.Close(); err != nil {
		return Result{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, &body)
	if err != nil {
		return Result{}, err
	}
	httpRequest.Header.Set("Content-Type", writer.FormDataContentType())
	if request.SampleRate > 0 {
		httpRequest.Header.Set("X-Sample-Rate", strconv.Itoa(request.SampleRate))
	}
	if request.Language != "" {
		httpRequest.Header.Set("X-Language", request.Language)
	}
	if p.APIKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return Result{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, &UpstreamError{Status: response.StatusCode, Message: fmt.Sprintf("stt provider returned %s", response.Status)}
	}
	var result Result
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func contentType(codec string) string {
	return contentTypeForCodec(codec)
}

func contentTypeForCodec(codec string) string {
	switch codec {
	case "opus", "opus_fs320":
		return "audio/ogg"
	case "pcm8", "pcm16", "pcm_s16le":
		return "audio/raw"
	default:
		return "application/octet-stream"
	}
}

func sanitizeFilename(filename string) string {
	var b strings.Builder
	for _, r := range filename {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	value := strings.TrimLeft(b.String(), ".")
	if len(value) > 64 {
		value = value[:64]
	}
	if value == "" {
		return "audio.wav"
	}
	return value
}
