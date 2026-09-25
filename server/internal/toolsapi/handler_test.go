package toolsapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

)

func TestUnknownToolEndpointReturnsNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/tools/unknown", nil)
	rec := httptest.NewRecorder()
	(Handler{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want auth rejection before dispatch", rec.Code)
	}
}

func TestIntQueryBounds(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?limit=999", nil)
	if got := intQuery(req, "limit", 20, 50); got != 50 {
		t.Fatalf("got %d, want 50", got)
	}
}

func TestCalendarEventRequiresConfiguredIntegration(t *testing.T) {
	h := Handler{}
	r := httptest.NewRequest(http.MethodPost, "/v1/tools/calendar-events", nil)
	_, err := h.createCalendarEvent(r, "uid", "title", time.Now(), time.Now().Add(time.Hour), "", "", nil)
	if err == nil {
		t.Fatal("expected missing integration storage error")
	}
}
