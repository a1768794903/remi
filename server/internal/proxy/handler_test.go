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

func TestGeminiUsesPerRequestBYOKKey(t *testing.T) {
	var gotKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-goog-api-key")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	h := Handler{GeminiBase: upstream.URL, GeminiAPIKey: "server-key", Client: upstream.Client()}
	r := httptest.NewRequest(http.MethodPost, "/v1/proxy/gemini/models/gemini-2.5-flash:generateContent", strings.NewReader(`{"x":1}`))
	r.Header.Set("X-LLM-BYOK-Key", "user-key")
	w := httptest.NewRecorder()
	h.Gemini(w, r)
	if w.Code != http.StatusOK || gotKey != "user-key" {
		t.Fatalf("code=%d key=%q", w.Code, gotKey)
	}
}

func TestGeminiFallsBackAfterProviderUnavailable(t *testing.T) {
	paths := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if len(paths) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"provisioned throughput capacity exhausted"}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
	}))
	defer upstream.Close()
	h := Handler{GeminiBase: upstream.URL, GeminiAPIKey: "secret", Client: upstream.Client()}
	r := httptest.NewRequest(http.MethodPost, "/v1/proxy/gemini/models/gemini-2.5-pro:generateContent", strings.NewReader(`{"contents":[]}`))
	w := httptest.NewRecorder()
	h.Gemini(w, r)
	if w.Code != http.StatusOK || len(paths) != 2 || !strings.Contains(paths[1], "gemini-2.5-flash-lite") {
		t.Fatalf("fallback code=%d paths=%v body=%q", w.Code, paths, w.Body.String())
	}
}

func TestProxyStatusMatchesProviderFailureContract(t *testing.T) {
	tests := []struct {
		upstream, status int
		code             string
		retryable        bool
	}{
		{429, 429, "provider_rate_limited", true},
		{408, 504, "provider_timeout", false},
		{503, 503, "provider_unavailable", true},
		{500, 502, "provider_unavailable", false},
		{400, 400, "provider_rejected", false},
	}
	for _, test := range tests {
		status, code, retryable, _ := proxyStatus(test.upstream)
		if status != test.status || code != test.code || retryable != test.retryable {
			t.Fatalf("upstream=%d got (%d,%q,%v)", test.upstream, status, code, retryable)
		}
	}
}

func TestClassifyProviderFailureKeepsFallbackNarrow(t *testing.T) {
	if got := classifyProviderFailure(http.StatusNotFound, []byte(`{"error":{"message":"publisher model not found"}}`)); got != "model_unavailable" {
		t.Fatalf("model unavailable classification=%q", got)
	}
	if !fallbackEligible(http.StatusNotFound, []byte(`{"error":{"message":"publisher model not found"}}`), false) {
		t.Fatal("model unavailable should be fallback eligible")
	}
	if got := classifyProviderFailure(http.StatusServiceUnavailable, []byte(`{"error":{"message":"temporary upstream failure"}}`)); got != "provider_error" {
		t.Fatalf("generic 5xx classification=%q", got)
	}
	if fallbackEligible(http.StatusServiceUnavailable, []byte(`{"error":{"message":"temporary upstream failure"}}`), false) {
		t.Fatal("generic 5xx must not select a different model")
	}
	if !fallbackEligible(http.StatusTooManyRequests, []byte(`{"error":{"message":"provisioned throughput capacity exhausted"}}`), false) {
		t.Fatal("PT exhaustion should be fallback eligible")
	}
}

func TestParseGeminiUsageMetadata(t *testing.T) {
	got := parseUsage([]byte(`{"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":7,"cachedContentTokenCount":3}}`))
	if got != (usageCounts{Input: 12, Output: 7, Cached: 3}) {
		t.Fatalf("usage=%+v", got)
	}
	stream := []byte("data: {\"usageMetadata\":{\"promptTokenCount\":4,\"candidatesTokenCount\":2}}\n")
	got = parseUsage(stream)
	if got.Input != 4 || got.Output != 2 {
		t.Fatalf("stream usage=%+v", got)
	}
}

func TestUsageReaderPreservesStreamBytes(t *testing.T) {
	input := []byte(`{"candidates":[{"finishReason":"STOP"}]}`)
	reader := &usageReader{source: strings.NewReader(string(input)), limit: 1024}
	output, err := io.ReadAll(reader)
	if err != nil || string(output) != string(input) {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestGeminiPayloadOutcomeRequiresContentAndTerminal(t *testing.T) {
	content, terminal, providerError := geminiPayloadOutcome([]byte(`{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}]}`))
	if !content || !terminal || providerError {
		t.Fatalf("success outcome=(%v,%v,%v)", content, terminal, providerError)
	}
	content, terminal, providerError = geminiPayloadOutcome([]byte(`{"candidates":[{"content":{"parts":[]}}]}`))
	if content || terminal || providerError {
		t.Fatalf("empty outcome=(%v,%v,%v)", content, terminal, providerError)
	}
	_, _, providerError = geminiPayloadOutcome([]byte(`{"error":{"code":503}}`))
	if !providerError {
		t.Fatal("provider error was not observed")
	}
}
