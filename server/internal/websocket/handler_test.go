package websocket

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"remi/server/internal/audio"
	"remi/server/internal/auth"
)

type fakeSessionStore struct {
	started  int
	finished int
}

func (s *fakeSessionStore) StartSession(context.Context, string, string, time.Time) (string, error) {
	s.started++
	return "conversation-1", nil
}

func (s *fakeSessionStore) FinishSession(context.Context, string, string, time.Time) error {
	s.finished++
	return nil
}

func TestAudioWebSocketCountsBinaryFrames(t *testing.T) {
	tracker := audio.NewTracker()
	handler := auth.Middleware("dev")(NewHandler(tracker))
	server := httptest.NewServer(handler)
	defer server.Close()

	url := "ws" + server.URL[len("http"):] + "/v1/audio/stream?session_id=test-session&device_id=device-1&codec=opus_fs320"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	messageType, payload, err := conn.ReadMessage()
	if err != nil || messageType != websocket.TextMessage {
		t.Fatalf("stats message: type=%d err=%v", messageType, err)
	}
	if len(payload) == 0 {
		t.Fatal("empty stats message")
	}
}

func TestAudioWebSocketPersistsSessionLifecycle(t *testing.T) {
	store := &fakeSessionStore{}
	handler := auth.Middleware("dev")(NewHandler(audio.NewTracker(), store))
	server := httptest.NewServer(handler)
	defer server.Close()
	url := "ws" + server.URL[len("http"):] + "/v4/listen?session_id=persisted-session"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	time.Sleep(20 * time.Millisecond)
	if store.started != 1 || store.finished != 1 {
		t.Fatalf("unexpected lifecycle: started=%d finished=%d", store.started, store.finished)
	}
}
