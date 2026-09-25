package trigger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"remi/server/internal/auth"
)

const (
	minSampleRate = 8000
	maxSampleRate = 48000
	maxBuffered   = 20 << 20
)

// Handler implements the pusher-side trigger socket. It preserves the Python
// wire protocol (little-endian uint32 frame type followed by a type-specific
// payload) and drains bounded transcript/audio queues on disconnect.
type Handler struct {
	DB      *sql.DB
	Redis   *redis.Client
	HTTP    *http.Client
	Upgrade websocket.Upgrader
}

func NewHandler(client *redis.Client) Handler {
	return Handler{Redis: client, HTTP: &http.Client{Timeout: 30 * time.Second}, Upgrade: websocket.Upgrader{ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10, CheckOrigin: func(*http.Request) bool { return true }}}
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	rate, err := strconv.Atoi(r.URL.Query().Get("sample_rate"))
	if err != nil || rate == 0 {
		rate = 8000
	}
	if rate < minSampleRate || rate > maxSampleRate {
		http.Error(w, "invalid sample rate", http.StatusBadRequest)
		return
	}
	conn, err := h.Upgrade.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	audioDelay := h.audioWebhookDelay(r.Context(), uid)
	appAudioEnabled := h.audioAppEnabled(r.Context(), uid)
	audioQueue := make(chan []byte, 20)
	appAudioQueue := make(chan []byte, 20)
	transcriptQueue := make(chan transcriptItem, 50)
	var webhookAudio []byte
	var appAudio []byte
	var workers sync.WaitGroup
	workers.Add(3)
	go func() { defer workers.Done(); h.audioWorker(r.Context(), uid, rate, audioDelay, audioQueue) }()
	go func() { defer workers.Done(); h.appAudioWorker(r.Context(), uid, rate, appAudioQueue) }()
	go func() { defer workers.Done(); h.transcriptWorker(r.Context(), uid, transcriptQueue) }()
	defer func() {
		if len(webhookAudio) > 0 {
			select {
			case audioQueue <- append([]byte(nil), webhookAudio...):
			default:
			}
		}
		if len(appAudio) > 0 {
			select {
			case appAudioQueue <- append([]byte(nil), appAudio...):
			default:
			}
		}
		close(audioQueue)
		close(appAudioQueue)
		close(transcriptQueue)
		workers.Wait()
	}()

	flush := func(buf *[]byte, size int, queue chan<- []byte) {
		if len(*buf) < size {
			return
		}
		chunk := append([]byte(nil), (*buf)...)
		*buf = (*buf)[:0]
		select {
		case queue <- chunk:
		default:
		}
	}
	for {
		kind, data, readErr := conn.ReadMessage()
		if readErr != nil {
			return
		}
		if kind != websocket.BinaryMessage {
			continue
		}
		frameType, payload, parseErr := parseFrame(data)
		if parseErr != nil {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseUnsupportedData, parseErr.Error()), time.Now().Add(time.Second))
			return
		}
		switch frameType {
		case 100:
		case 101:
			if len(payload) < 8 {
				return
			}
			pcm := payload[8:]
			if len(webhookAudio)+len(pcm) <= maxBuffered {
				webhookAudio = append(webhookAudio, pcm...)
			}
			if appAudioEnabled && len(appAudio)+len(pcm) <= maxBuffered {
				appAudio = append(appAudio, pcm...)
			}
			if audioDelay > 0 {
				flush(&webhookAudio, rate*2*audioDelay, audioQueue)
			}
			flush(&appAudio, rate*2*4, appAudioQueue)
		case 102:
			var input struct {
				Segments []map[string]any `json:"segments"`
				MemoryID string           `json:"memory_id"`
			}
			if json.Unmarshal(payload, &input) != nil || input.Segments == nil {
				return
			}
			select {
			case transcriptQueue <- transcriptItem{Segments: input.Segments, MemoryID: input.MemoryID}:
			default:
			}
		case 103, 104, 105:
			// Conversation/finalization/speaker-sample frames are owned by the
			// listen session. Trigger sockets only consume transcript/audio data.
		}
	}
}

type transcriptItem struct {
	Segments []map[string]any
	MemoryID string
}

func parseFrame(data []byte) (uint32, []byte, error) {
	if len(data) < 4 {
		return 0, nil, fmt.Errorf("frame header is incomplete")
	}
	kind := binary.LittleEndian.Uint32(data[:4])
	if kind < 100 || kind > 105 {
		return 0, nil, fmt.Errorf("unknown frame type")
	}
	if kind == 101 && len(data) < 12 {
		return 0, nil, fmt.Errorf("audio frame is incomplete")
	}
	return kind, data[4:], nil
}

func (h Handler) audioWebhookDelay(ctx context.Context, uid string) int {
	if h.Redis == nil {
		return 0
	}
	value, err := h.Redis.Get(ctx, "users:"+uid+":developer:webhook:audio_bytes").Result()
	if err != nil {
		return 0
	}
	parts := strings.Split(value, ",")
	if len(parts) < 2 {
		return 0
	}
	seconds, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
	if seconds < 1 || seconds > 60 {
		return 0
	}
	return seconds
}

func (h Handler) audioWorker(ctx context.Context, uid string, rate, delay int, queue <-chan []byte) {
	for data := range queue {
		if len(data) == 0 {
			continue
		}
		if delay == 0 {
			continue
		}
		value, err := h.Redis.Get(ctx, "users:"+uid+":developer:webhook:audio_bytes").Result()
		if err != nil {
			continue
		}
		endpoint := strings.TrimSpace(strings.Split(value, ",")[0])
		parsed, err := url.Parse(endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			continue
		}
		q := parsed.Query()
		q.Set("uid", uid)
		q.Set("sample_rate", strconv.Itoa(rate))
		parsed.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(data))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		_, _ = h.client().Do(req)
	}
}

func (h Handler) audioAppEnabled(ctx context.Context, uid string) bool {
	if h.DB == nil {
		return false
	}
	var one int
	err := h.DB.QueryRowContext(ctx, `SELECT 1 FROM plugins_data p JOIN user_enabled_apps e ON e.app_id=p.id AND e.user_external_uid=? WHERE p.status='approved' AND COALESCE(p.disabled,FALSE)=FALSE AND JSON_UNQUOTE(JSON_EXTRACT(p.external_integration,'$.triggers_on'))='audio_bytes' AND JSON_UNQUOTE(JSON_EXTRACT(p.external_integration,'$.webhook_url')) IS NOT NULL LIMIT 1`, uid).Scan(&one)
	return err == nil && one == 1
}

func (h Handler) appAudioWorker(ctx context.Context, uid string, rate int, queue <-chan []byte) {
	for data := range queue {
		if len(data) == 0 || h.DB == nil {
			continue
		}
		rows, err := h.DB.QueryContext(ctx, `SELECT p.id, JSON_UNQUOTE(JSON_EXTRACT(p.external_integration,'$.webhook_url')) FROM plugins_data p JOIN user_enabled_apps e ON e.app_id=p.id AND e.user_external_uid=? WHERE p.status='approved' AND COALESCE(p.disabled,FALSE)=FALSE AND JSON_UNQUOTE(JSON_EXTRACT(p.external_integration,'$.triggers_on'))='audio_bytes' AND JSON_UNQUOTE(JSON_EXTRACT(p.external_integration,'$.webhook_url')) IS NOT NULL`, uid)
		if err != nil {
			continue
		}
		for rows.Next() {
			var appID, endpoint string
			if rows.Scan(&appID, &endpoint) != nil {
				continue
			}
			if !safeWebhookURL(endpoint) {
				continue
			}
			u, _ := url.Parse(endpoint)
			q := u.Query()
			q.Set("sample_rate", strconv.Itoa(rate))
			q.Set("uid", uid)
			u.RawQuery = q.Encode()
			req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(data))
			if reqErr != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("X-Omi-App-ID", appID)
			resp, doErr := h.client().Do(req)
			if doErr == nil && resp != nil {
				resp.Body.Close()
			}
		}
		rows.Close()
	}
}

func safeWebhookURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
	}
	return true
}

func (h Handler) transcriptWorker(ctx context.Context, uid string, queue <-chan transcriptItem) {
	for item := range queue {
		if h.Redis == nil {
			continue
		}
		endpoint, err := h.Redis.Get(ctx, "users:"+uid+":developer:webhook:realtime_transcript").Result()
		if err != nil {
			continue
		}
		parsed, err := url.Parse(strings.TrimSpace(endpoint))
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		payload, _ := json.Marshal(map[string]any{"uid": uid, "segments": item.Segments, "memory_id": item.MemoryID})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(payload))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		_, _ = h.client().Do(req)
	}
}

func (h Handler) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return http.DefaultClient
}
