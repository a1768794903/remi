package websocket

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"remi/server/internal/audio"
	"remi/server/internal/auth"
)

type Handler struct {
	Tracker *audio.Tracker
	Upgrade websocket.Upgrader
}

func NewHandler(tracker *audio.Tracker) *Handler {
	return &Handler{
		Tracker: tracker,
		Upgrade: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
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
	defer func() {
		if session, ok := h.Tracker.Finish(sessionID); ok {
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
