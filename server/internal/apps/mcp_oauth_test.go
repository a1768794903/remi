package apps

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPOAuthCallbackRejectsMalformedState(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/apps/mcp/callback?code=code&state=malformed", nil)
	(Handler{}).MCPOAuthCallback(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if got := w.Body.String(); got == "" || !strings.Contains(got, "Invalid state parameter") {
		t.Fatalf("body = %q", got)
	}
}
