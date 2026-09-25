package stt

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type recordingProvider struct{ request Request }

func (p *recordingProvider) Transcribe(_ context.Context, request Request) (Result, error) {
	p.request = request
	return Result{Text: "hello", Segments: []Segment{{Text: "hello", StartSec: 0, EndSec: 1}}}, nil
}

func TestHandlerTranscribeMultipart(t *testing.T) {
	provider := &recordingProvider{}
	body := &strings.Builder{}
	w := multipart.NewWriter(body)
	if err := w.WriteField("codec", "pcm16"); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("sample_rate", "16000"); err != nil {
		t.Fatal(err)
	}
	part, err := w.CreateFormFile("file", "voice.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, "audio")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/stt/transcribe", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	res := httptest.NewRecorder()
	(Handler{Provider: provider}).Transcribe(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if string(provider.request.Audio) != "audio" || provider.request.SampleRate != 16000 || provider.request.Codec != "pcm16" {
		t.Fatalf("request=%+v", provider.request)
	}
}

func TestHandlerRejectsMissingFile(t *testing.T) {
	form := url.Values{"codec": {"pcm16"}}
	req := httptest.NewRequest(http.MethodPost, "/v1/stt/transcribe", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	(Handler{Provider: &recordingProvider{}}).Transcribe(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", res.Code)
	}
}
