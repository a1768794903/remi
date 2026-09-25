package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"remi/server/internal/audio"
	"remi/server/internal/auth"
	"remi/server/internal/stt"
	"remi/server/internal/transcripts"
)

type SessionStore interface {
	StartSession(context.Context, string, string, time.Time) (string, error)
	FinishSession(context.Context, string, string, time.Time) error
}

type SegmentStore interface {
	Create(context.Context, string, string, transcripts.CreateInput) (transcripts.Segment, error)
}

type Handler struct {
	Tracker      *audio.Tracker
	Upgrade      websocket.Upgrader
	SessionStore SessionStore
	SegmentStore SegmentStore
	STT          stt.Provider
}

func NewHandler(tracker *audio.Tracker, stores ...SessionStore) *Handler {
	var store SessionStore
	if len(stores) > 0 {
		store = stores[0]
	}
	return &Handler{
		Tracker:      tracker,
		Upgrade:      websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		SessionStore: store,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	conn, err := h.Upgrade.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	sessionID := queryOrDefault(r, "session_id", "session-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	deviceID := queryOrDefault(r, "device_id", "unknown-device")
	codec := queryOrDefault(r, "codec", "unknown")
	sampleRate, _ := strconv.Atoi(queryOrDefault(r, "sample_rate", "16000"))
	h.Tracker.Start(sessionID, userID, deviceID, codec, sampleRate, time.Now())
	var audioBuffer bytes.Buffer
	var conversationID string
	if h.SessionStore != nil {
		conversationID, _ = h.SessionStore.StartSession(r.Context(), userID, deviceID, time.Now().UTC())
	}
	defer func() {
		if session, ok := h.Tracker.Finish(sessionID); ok {
			if h.STT != nil && h.SegmentStore != nil && conversationID != "" && audioBuffer.Len() > 0 {
				result, transcribeErr := h.STT.Transcribe(context.Background(), stt.Request{Audio: audioBuffer.Bytes(), SampleRate: session.SampleRate, Codec: session.Codec})
				if transcribeErr == nil {
					for _, segment := range result.Segments {
						_, _ = h.SegmentStore.Create(context.Background(), userID, conversationID, transcripts.CreateInput{Speaker: segment.Speaker, Text: segment.Text, StartMs: int64(segment.StartSec * 1000), EndMs: int64(segment.EndSec * 1000), Source: "stt"})
					}
				}
			}
			if h.SessionStore != nil && conversationID != "" {
				_ = h.SessionStore.FinishSession(context.Background(), userID, conversationID, time.Now().UTC())
			}
			_ = conn.WriteJSON(map[string]any{"type": "session_closed", "session_id": session.ID, "received_bytes": session.ReceivedBytes, "received_packets": session.ReceivedPackets})
		}
	}()

	_ = conn.WriteJSON(map[string]any{"type": "session_started", "session_id": sessionID, "user_id": userID, "device_id": deviceID})
	conn.SetReadLimit(8 << 20)
	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		session, ok := h.Tracker.AddPacket(sessionID, len(payload), time.Now())
		if !ok {
			return
		}
		_, _ = audioBuffer.Write(payload)
		_ = conn.WriteJSON(map[string]any{"type": "audio_stats", "session_id": session.ID, "received_bytes": session.ReceivedBytes, "received_packets": session.ReceivedPackets})
	}
}

func queryOrDefault(r *http.Request, key, fallback string) string {
	if value := r.URL.Query().Get(key); value != "" {
		return value
	}
	return fallback
}

var _ = json.Valid
