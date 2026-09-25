package integrations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"remi/server/internal/auth"
	"remi/server/internal/chat"
)

type synthesisProvider struct {
	answer string
}

func (p synthesisProvider) Complete(_ context.Context, _ []chat.Turn) (string, error) {
	return p.answer, nil
}

func TestSynthesizeConnectorRequiresProviderAndReturnsStructuredItems(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/connectors/synthesize", strings.NewReader(`{"source":"notes","items":["Buy milk"]}`))
	rec := httptest.NewRecorder()
	auth.Middleware("dev")(http.HandlerFunc(Handler{Provider: synthesisProvider{answer: `{"memories":["Prefers oat milk"],"tasks":[{"description":"Buy milk","priority":"high"}],"profile":""}`}}.Synthesize)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Prefers oat milk") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSynthesizeConnectorRejectsInvalidPayload(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/connectors/synthesize", strings.NewReader(`{"source":"","items":[]}`))
	rec := httptest.NewRecorder()
	auth.Middleware("dev")(http.HandlerFunc(Handler{Provider: synthesisProvider{answer: `{}`}}.Synthesize)).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
