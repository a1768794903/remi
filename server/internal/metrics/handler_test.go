package metrics

import (
	"net/http/httptest"
	"testing"
)

func TestMetricsRequiresSecret(t *testing.T) {
	t.Setenv("METRICS_SECRET", "secret")
	r := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	(Handler{}).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("status=%d", w.Code)
	}
	r.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	(Handler{}).ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Content-Type") == "" {
		t.Fatalf("status=%d content-type=%q", w.Code, w.Header().Get("Content-Type"))
	}
}
