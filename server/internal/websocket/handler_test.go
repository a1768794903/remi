package websocket

import (
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"remi/server/internal/audio"
	"remi/server/internal/auth"
)

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
