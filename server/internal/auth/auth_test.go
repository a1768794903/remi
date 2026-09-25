package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDevMiddlewareUsesHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, err := UserID(r.Context())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(uid))
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Remi-User-ID", "firebase-user")
	res := httptest.NewRecorder()
	Middleware("dev")(next).ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "firebase-user" {
		t.Fatalf("status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestProductionMiddlewareDoesNotAcceptDevHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Remi-User-ID", "should-not-pass")
	res := httptest.NewRecorder()
	MiddlewareWithVerifier("production", NewFirebaseVerifier("project"))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", res.Code)
	}
}

func TestFirebaseVerifierRequiresProject(t *testing.T) {
	if _, err := (&FirebaseVerifier{}).Verify(t.Context(), "token"); err == nil {
		t.Fatal("expected missing project error")
	}
}
