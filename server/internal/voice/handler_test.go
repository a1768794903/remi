package voice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"remi/server/internal/stt"
)

type provider struct{}

func (provider) Transcribe(context.Context, stt.Request) (stt.Result, error) {
	return stt.Result{Text: "hello"}, nil
}

func TestRawTranscription(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v2/voice-message/transcribe?sample_rate=16000&channels=1", strings.NewReader("audio"))
	r.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	(Handler{Provider: provider{}}).Transcribe(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || out["transcript"] != "hello" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
func TestRawValidation(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v2/voice-message/transcribe?sample_rate=7000", strings.NewReader("audio"))
	r.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	(Handler{Provider: provider{}}).Transcribe(w, r)
	if w.Code != 400 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestStreamFinalize(t *testing.T) {
	h := Handler{Provider: provider{}}
	ts := httptest.NewServer(http.HandlerFunc(h.Stream))
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v2/voice-message/transcribe-stream"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("audio")); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("finalize")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var segments []stt.Segment
	if err := conn.ReadJSON(&segments); err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 || segments[0].Text != "hello" {
		t.Fatalf("segments=%#v", segments)
	}
}
