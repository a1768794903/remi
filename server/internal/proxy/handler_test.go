package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiForwardsPathQueryBodyAndKey(t *testing.T) {
	var gotPath, gotKey, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		gotKey = r.Header.Get("x-goog-api-key")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("X-Upstream", "ok")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("stream"))
	}))
	defer upstream.Close()
	h := Handler{GeminiBase: upstream.URL, GeminiAPIKey: "secret", Client: upstream.Client()}
	r := httptest.NewRequest(http.MethodPost, "/v1/proxy/gemini/models/gemini-2.5-flash:generateContent?a=1", strings.NewReader(`{"x":1}`))
	w := httptest.NewRecorder()
	h.Gemini(w, r)
	if w.Code != 201 || gotPath != "/models/gemini-2.5-flash:generateContent?a=1" || gotKey != "secret" || gotBody != `{"x":1}` || w.Header().Get("X-Upstream") != "ok" {
		t.Fatalf("forward mismatch: code=%d path=%q key=%q body=%q header=%q", w.Code, gotPath, gotKey, gotBody, w.Header().Get("X-Upstream"))
	}
}

func TestGeminiRejectsUnknownModelBeforeUpstream(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer upstream.Close()
	h := Handler{GeminiBase: upstream.URL, Client: upstream.Client()}
	r := httptest.NewRequest(http.MethodPost, "/v1/proxy/gemini/models/unknown:generateContent", strings.NewReader(`{"x":1}`))
	w := httptest.NewRecorder()
	h.Gemini(w, r)
	if w.Code != http.StatusForbidden || called {
		t.Fatalf("unknown model should be rejected: code=%d called=%v", w.Code, called)
	}
}

func TestGeminiCapsManagedOutputTokens(t *testing.T) {
	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	h := Handler{GeminiBase: upstream.URL, GeminiAPIKey: "secret", Client: upstream.Client()}
	r := httptest.NewRequest(http.MethodPost, "/v1/proxy/gemini/models/gemini-2.5-flash:generateContent", strings.NewReader(`{"generationConfig":{"maxOutputTokens":9999}}`))
	w := httptest.NewRecorder()
	h.Gemini(w, r)
	config, _ := got["generationConfig"].(map[string]any)
	if gotTokens, _ := config["maxOutputTokens"].(float64); gotTokens != managedGeminiOutputTokens {
		t.Fatalf("maxOutputTokens=%v", gotTokens)
	}
}
