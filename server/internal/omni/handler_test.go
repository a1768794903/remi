package omni

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gorilla/websocket"
	"remi/server/internal/auth"
)

func TestUpstreamURLUsesBYOKAndDoesNotAllowHTTP(t *testing.T) {
	t.Setenv("OMNI_OPENAI_WS_URL", "http://localhost:1234")
	if _, _, err := upstreamURL("openai", "m", http.Header{"X-Byok-Openai": []string{"secret"}}); err == nil {
		t.Fatal("expected non-websocket endpoint to be rejected")
	}
	t.Setenv("OMNI_OPENAI_WS_URL", "ws://localhost:1234/realtime")
	endpoint, headers, err := upstreamURL("openai", "m", http.Header{"X-Byok-Openai": []string{"secret"}})
	if err != nil || endpoint != "ws://localhost:1234/realtime" || headers.Get("Authorization") != "Bearer secret" {
		t.Fatalf("unexpected upstream: %q %q %v", endpoint, headers.Get("Authorization"), err)
	}
}

func TestRelayCopiesTextFrames(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		kind, data, err := conn.ReadMessage()
		if err == nil {
			_ = conn.WriteMessage(kind, append([]byte("echo:"), data...))
		}
	}))
	defer upstream.Close()
	wsURL := "ws" + upstream.URL[len("http"):]
	t.Setenv("OMNI_OPENAI_WS_URL", wsURL)
	t.Setenv("OMNI_ALLOW_INSECURE_UPSTREAM", "1")

	h := NewHandler()
	server := httptest.NewServer(auth.Middleware("dev")(http.HandlerFunc(h.ServeHTTP)))
	defer server.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):]+"?provider=openai", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	kind, got, err := client.ReadMessage()
	if err != nil || kind != websocket.TextMessage || string(got) != "echo:hello" {
		t.Fatalf("got kind=%d data=%q err=%v", kind, got, err)
	}
}

func TestRelayRequiresAuthenticatedContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/omni/relay?provider=openai", nil).WithContext(context.Background())
	rr := httptest.NewRecorder()
	NewHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestNoAccidentalEnvironmentMutation(t *testing.T) {
	_ = os.Getenv("OMNI_OPENAI_WS_URL")
}
