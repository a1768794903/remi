package omni

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"remi/server/internal/auth"
)

// Handler is the server-side provider relay used by the desktop floating bar.
// Provider messages are intentionally opaque: the client speaks the provider's
// native realtime protocol and this service only authenticates, selects the
// configured upstream, and copies frames in both directions.
type Handler struct {
	Upgrade websocket.Upgrader
	Dialer  websocket.Dialer
}

func NewHandler() Handler {
	return Handler{
		Upgrade: websocket.Upgrader{ReadBufferSize: 32 << 10, WriteBufferSize: 32 << 10, CheckOrigin: func(*http.Request) bool { return true }},
		Dialer:  websocket.Dialer{},
	}
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.UserID(r.Context()); err != nil {
		http.Error(w, "missing authenticated user", http.StatusUnauthorized)
		return
	}
	provider := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
	if provider == "" {
		provider = "gemini"
	}
	if provider != "gemini" && provider != "openai" {
		http.Error(w, "unsupported provider", http.StatusBadRequest)
		return
	}
	upstream, headers, err := upstreamURL(provider, r.URL.Query().Get("model"), r.Header)
	if err != nil {
		// Do not accept the client socket if the relay cannot establish a
		// provider connection; this mirrors the Python route's fail-closed
		// admission behavior and avoids a misleading successful upgrade.
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	client, err := h.Upgrade.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer client.Close()
	upstreamConn, response, err := h.Dialer.DialContext(r.Context(), upstream, headers)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		_ = client.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "provider unavailable"), time.Now().Add(time.Second))
		return
	}
	defer upstreamConn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var once sync.Once
	closeBoth := func() { once.Do(func() { _ = client.Close(); _ = upstreamConn.Close() }) }
	copyFrames := func(dst, src *websocket.Conn) {
		defer cancel()
		for {
			kind, payload, readErr := src.ReadMessage()
			if readErr != nil {
				return
			}
			if writeErr := dst.WriteMessage(kind, payload); writeErr != nil {
				return
			}
		}
	}
	go func() { copyFrames(upstreamConn, client); closeBoth() }()
	go func() { copyFrames(client, upstreamConn); closeBoth() }()
	<-ctx.Done()
}

func upstreamURL(provider, model string, incoming http.Header) (string, http.Header, error) {
	var endpoint string
	var key string
	switch provider {
	case "gemini":
		endpoint = os.Getenv("OMNI_GEMINI_WS_URL")
		key = strings.TrimSpace(incoming.Get("x-byok-gemini"))
		if key == "" {
			key = os.Getenv("GEMINI_API_KEY")
		}
		if endpoint == "" {
			endpoint = "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"
		}
	case "openai":
		endpoint = os.Getenv("OMNI_OPENAI_WS_URL")
		key = strings.TrimSpace(incoming.Get("x-byok-openai"))
		if key == "" {
			key = os.Getenv("OPENAI_API_KEY")
		}
		if endpoint == "" {
			modelValue := model
			if modelValue == "" {
				modelValue = "gpt-realtime-2"
			}
			endpoint = "wss://api.openai.com/v1/realtime?model=" + url.QueryEscape(modelValue)
		}
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" {
		return "", nil, fmt.Errorf("invalid %s realtime endpoint", provider)
	}
	if key == "" && os.Getenv("OMNI_ALLOW_INSECURE_UPSTREAM") != "1" {
		return "", nil, fmt.Errorf("%s provider key is not configured", provider)
	}
	headers := http.Header{}
	if provider == "openai" && key != "" {
		headers.Set("Authorization", "Bearer "+key)
	}
	if provider == "gemini" && key != "" && parsed.Query().Get("key") == "" {
		q := parsed.Query()
		q.Set("key", key)
		parsed.RawQuery = q.Encode()
	}
	return parsed.String(), headers, nil
}
