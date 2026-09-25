package taskintelligence

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"remi/server/internal/auth"
)

func TestStableIDsAreDeterministicAndScoped(t *testing.T) {
	if stable("x", "u", 1, "k") != stable("x", "u", 1, "k") {
		t.Fatal("not deterministic")
	}
	if stable("x", "u", 1, "k") == stable("x", "other", 1, "k") {
		t.Fatal("not user scoped")
	}
}

func TestMutationRequiresIdempotency(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := Handler{DB: db}
	req := httptest.NewRequest(http.MethodPost, "/v1/task-intelligence/interventions", strings.NewReader(`{}`))
	wrapped := auth.Middleware("dev")(http.HandlerFunc(h.Intervention))
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("got %d", rr.Code)
	}
}
