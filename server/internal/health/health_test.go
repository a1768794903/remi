package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	res := httptest.NewRecorder()
	Handler(func() bool { return false }).ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", res.Code, http.StatusOK)
	}
}

func TestReadyzRequiresDependencies(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	res := httptest.NewRecorder()
	Handler(func() bool { return false }).ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want %d", res.Code, http.StatusServiceUnavailable)
	}
}
