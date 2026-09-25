package tts

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerRejectsEmptyTextBeforeCallingProvider(t *testing.T) {
	called := false
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer provider.Close()

	h := Handler{APIKey: "test-key", Endpoint: provider.URL, Client: provider.Client()}
	req := httptest.NewRequest(http.MethodPost, "/v1/tts/synthesize", strings.NewReader(`{"text":"  ","voice_id":"alloy"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	h.Synthesize(res, req)

	if res.Code != http.StatusBadRequest || called {
		t.Fatalf("expected 400 without provider call, got status=%d called=%v body=%s", res.Code, called, res.Body.String())
	}
}

func TestHandlerReturnsProviderAudioAndSendsOpenAITTSContract(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/speech" {
			t.Fatalf("unexpected provider request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing authorization header")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"gpt-4o-mini-tts"`) || !strings.Contains(string(body), `"voice":"nova"`) {
			t.Fatalf("unexpected request body: %s", body)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("mp3"))
	}))
	defer provider.Close()

	h := Handler{APIKey: "test-key", Endpoint: provider.URL + "/v1/audio/speech", Client: provider.Client()}
	req := httptest.NewRequest(http.MethodPost, "/v1/tts/synthesize", strings.NewReader(`{"text":"hello","voice_id":"nova","instructions":"warm"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	h.Synthesize(res, req)

	if res.Code != http.StatusOK || res.Header().Get("Content-Type") != "audio/mpeg" || res.Body.String() != "mp3" {
		t.Fatalf("unexpected response: status=%d content-type=%q body=%q", res.Code, res.Header().Get("Content-Type"), res.Body.String())
	}
}

func TestHandlerRetriesTransientProviderFailure(t *testing.T) {
	attempts := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer provider.Close()

	h := Handler{APIKey: "test-key", Endpoint: provider.URL, Client: provider.Client()}
	req := httptest.NewRequest(http.MethodPost, "/v1/tts/synthesize", strings.NewReader(`{"text":"hello","voice_id":"alloy"}`))
	res := httptest.NewRecorder()
	h.Synthesize(res, req)

	if res.Code != http.StatusOK || attempts != 3 {
		t.Fatalf("expected third-attempt success, status=%d attempts=%d", res.Code, attempts)
	}
}

func TestHandlerRejectsMissingConfiguration(t *testing.T) {
	h := Handler{}
	req := httptest.NewRequest(http.MethodPost, "/v1/tts/synthesize", strings.NewReader(`{"text":"hello","voice_id":"alloy"}`))
	res := httptest.NewRecorder()
	h.Synthesize(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", res.Code)
	}
}
