package stt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPProviderTranscribe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization header missing")
		}
		if r.Header.Get("X-Sample-Rate") != "16000" {
			t.Errorf("sample rate header missing")
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("expected multipart: %v", err)
		}
		seenFile, seenDiarize := false, false
		for {
			part, nextErr := reader.NextPart()
			if nextErr != nil {
				break
			}
			if part.FormName() == "file" {
				seenFile = true
			}
			if part.FormName() == "diarize" {
				seenDiarize = true
			}
		}
		if !seenFile || !seenDiarize {
			t.Fatalf("multipart fields missing: file=%v diarize=%v", seenFile, seenDiarize)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello","language":"en","segments":[{"text":"hello","start":0,"end":1}]}`))
	}))
	defer server.Close()
	result, err := (HTTPProvider{Endpoint: server.URL, APIKey: "test-key"}).Transcribe(context.Background(), Request{Audio: []byte{1, 2}, SampleRate: 16000, Codec: "pcm16", Filename: "../../unsafe.wav", Diarize: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello" || len(result.Segments) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestNullProviderIsSafeDevelopmentFallback(t *testing.T) {
	result, err := (NullProvider{}).Transcribe(context.Background(), Request{Audio: []byte{1}})
	if err != nil || result.Text != "" {
		t.Fatalf("unexpected null result: %+v %v", result, err)
	}
}
