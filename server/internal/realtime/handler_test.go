package realtime

import (
	"net/http"
	"net/http/httptest"
	"remi/server/internal/auth"
	"strings"
	"testing"
)

func TestCostUsesProviderModalityRates(t *testing.T) {
	got := cost("openai", "gpt-realtime-2", usage{InputText: 1_000_000, CachedText: 100_000, InputAudio: 1_000_000, OutputText: 1_000_000, OutputAudio: 1_000_000})
	const want = 3_600_000 + 40_000 + 32_000_000 + 24_000_000 + 64_000_000
	if got != want {
		t.Fatalf("cost=%d want=%d", got, want)
	}
}

func TestHandlerReturnsStructuredErrorForBadProvider(t *testing.T) {
	h := Handler{}
	req := httptest.NewRequest(http.MethodPost, "/v2/realtime/session", strings.NewReader(`{"provider":"other"}`))
	res := httptest.NewRecorder()
	auth.Middleware("dev")(http.HandlerFunc(h.Mint)).ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"reason":"bad_provider"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestHandlerRejectsUnconfiguredProvider(t *testing.T) {
	h := Handler{}
	req := httptest.NewRequest(http.MethodPost, "/v2/realtime/session", strings.NewReader(`{"provider":"openai"}`))
	res := httptest.NewRecorder()
	auth.Middleware("dev")(http.HandlerFunc(h.Mint)).ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), `"reason":"provider_not_configured"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
