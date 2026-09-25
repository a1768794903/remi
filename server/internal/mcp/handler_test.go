package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamGetDoesNotOpenLongLivedEmptySSE(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/mcp/sse", nil)
	rec := httptest.NewRecorder()
	(Handler{}).Stream(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
	if rec.Header().Get("Allow") != "POST, HEAD, DELETE" {
		t.Fatalf("Allow = %q", rec.Header().Get("Allow"))
	}
}

func TestStreamHeadAndDeleteRequireAuthentication(t *testing.T) {
	for _, method := range []string{http.MethodHead, http.MethodDelete} {
		req := httptest.NewRequest(method, "/v1/mcp/sse", nil)
		rec := httptest.NewRecorder()
		(Handler{}).Stream(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", method, rec.Code)
		}
	}
}
