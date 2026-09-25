package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"remi/server/internal/auth"
)

const maxGeminiBodyBytes int64 = 5 * 1024 * 1024
const maxGeminiOutputTokens = 8192
const managedGeminiOutputTokens = 2048

var allowedGeminiModels = map[string]bool{
	"gemini-2.5-flash": true, "gemini-2.5-flash-lite": true, "gemini-2.5-pro": true,
	"gemini-3.1-flash-lite": true, "gemini-embedding-001": true,
}
var allowedGeminiActions = map[string]bool{"generateContent": true, "streamGenerateContent": true, "embedContent": true, "batchEmbedContents": true}

// Handler forwards the desktop provider surfaces while keeping provider
// credentials on the server. It deliberately copies response headers and
// streams the response body so generateContent and streamGenerateContent have
// the same transport behavior.
type Handler struct {
	GeminiBase   string
	GeminiAPIKey string
	DeepgramBase string
	DeepgramKey  string
	Client       *http.Client
	Redis        *redis.Client
}

const geminiBurstLimit int64 = 30
const geminiDailyLimit int64 = 1500

func (h Handler) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 75 * time.Second}
}

func geminiPath(path string) (model, action string, err error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[0] != "models" {
		return "", "", fmt.Errorf("invalid Gemini path")
	}
	model, action = parts[1], ""
	if i := strings.LastIndexByte(model, ':'); i > 0 {
		action, model = model[i+1:], model[:i]
	}
	if !allowedGeminiModels[model] || !allowedGeminiActions[action] {
		return "", "", fmt.Errorf("Gemini model or action is not allowed")
	}
	return model, action, nil
}

func readGeminiBody(w http.ResponseWriter, r *http.Request, managed bool) ([]byte, error) {
	if r.ContentLength > maxGeminiBodyBytes {
		return nil, fmt.Errorf("request body is too large")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxGeminiBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxGeminiBodyBytes {
		return nil, fmt.Errorf("request body is too large")
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("request body is required")
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid JSON request body")
	}
	if managed {
		if generation, ok := payload["generationConfig"].(map[string]any); ok {
			if value, ok := generation["maxOutputTokens"].(float64); ok && value > managedGeminiOutputTokens {
				generation["maxOutputTokens"] = managedGeminiOutputTokens
			}
		}
	}
	return json.Marshal(payload)
}
func (h Handler) geminiBase() string {
	if h.GeminiBase != "" {
		return strings.TrimRight(h.GeminiBase, "/")
	}
	if v := os.Getenv("GEMINI_PROXY_ENDPOINT"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://generativelanguage.googleapis.com"
}
func (h Handler) geminiKey() string {
	if h.GeminiAPIKey != "" {
		return h.GeminiAPIKey
	}
	return os.Getenv("GEMINI_API_KEY")
}
func (h Handler) deepgramBase() string {
	if h.DeepgramBase != "" {
		return strings.TrimRight(h.DeepgramBase, "/")
	}
	if v := os.Getenv("DEEPGRAM_PROXY_ENDPOINT"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://api.deepgram.com"
}
func (h Handler) deepgramKey() string {
	if h.DeepgramKey != "" {
		return h.DeepgramKey
	}
	return os.Getenv("DEEPGRAM_API_KEY")
}

func copyHeaders(dst, src http.Header) {
	for k, values := range src {
		for _, v := range values {
			dst.Add(k, v)
		}
	}
}

func (h Handler) forward(w http.ResponseWriter, r *http.Request, target string, key string, bearer bool) {
	u, err := url.Parse(target)
	if err != nil {
		http.Error(w, "invalid provider endpoint", 500)
		return
	}
	u.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), r.Method, u.String(), r.Body)
	if err != nil {
		http.Error(w, "unable to create provider request", 500)
		return
	}
	copyHeaders(req.Header, r.Header)
	req.Header.Del("Host")
	req.Header.Del("Content-Length")
	req.Header.Del("Authorization")
	if key != "" {
		if bearer {
			req.Header.Set("Authorization", "Token "+key)
		} else {
			req.Header.Set("x-goog-api-key", key)
		}
	}
	resp, err := h.client().Do(req)
	if err != nil {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "provider unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (h Handler) Gemini(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/proxy/gemini")
	if path == "" || path == r.URL.Path {
		http.Error(w, "invalid Gemini path", 400)
		return
	}
	model, _, err := geminiPath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	path, managed, ok := h.meterGemini(w, r, path, model)
	if !ok {
		return
	}
	body, err := readGeminiBody(w, r, managed)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "too large") {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}
	_ = model
	r.Body = io.NopCloser(bytes.NewReader(body))
	h.forward(w, r, h.geminiBase()+path, h.geminiRequestKey(r), false)
}

func (h Handler) GeminiStream(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/proxy/gemini-stream")
	if path == "" || path == r.URL.Path {
		http.Error(w, "invalid Gemini path", 400)
		return
	}
	model, _, err := geminiPath(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	path, managed, ok := h.meterGemini(w, r, path, model)
	if !ok {
		return
	}
	body, err := readGeminiBody(w, r, managed)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "too large") {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	h.forward(w, r, h.geminiBase()+path, h.geminiRequestKey(r), false)
}

func (h Handler) meterGemini(w http.ResponseWriter, r *http.Request, path, model string) (string, bool, bool) {
	managed := r.Header.Get("X-LLM-BYOK-Key") == "" && os.Getenv("GEMINI_BYOK_ONLY") != "true"
	if !managed || h.Redis == nil {
		return path, managed, true
	}
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return "", false, false
	}
	now := time.Now().UTC()
	burstKey := fmt.Sprintf("remi:quota:gemini:burst:%s:%d", uid, now.Unix()/60)
	burst, err := h.Redis.Incr(r.Context(), burstKey).Result()
	if err != nil {
		http.Error(w, "Gemini rate limiter is unavailable", http.StatusServiceUnavailable)
		return "", false, false
	}
	if burst == 1 {
		_ = h.Redis.Expire(r.Context(), burstKey, 2*time.Minute).Err()
	}
	if burst > geminiBurstLimit {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", 60-now.Second()))
		w.Header().Set("X-Omi-Retryable", "true")
		http.Error(w, "Gemini request rate limit exceeded", http.StatusTooManyRequests)
		return "", false, false
	}
	dayKey := fmt.Sprintf("remi:quota:gemini:daily:%s:%s", uid, now.Format("2006-01-02"))
	daily, err := h.Redis.Incr(r.Context(), dayKey).Result()
	if err != nil {
		http.Error(w, "Gemini rate limiter is unavailable", http.StatusServiceUnavailable)
		return "", false, false
	}
	if daily == 1 {
		_ = h.Redis.Expire(r.Context(), dayKey, 48*time.Hour).Err()
	}
	if daily > geminiDailyLimit {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", 86400-(now.Hour()*3600+now.Minute()*60+now.Second())))
		w.Header().Set("X-Omi-Retryable", "false")
		http.Error(w, "Gemini daily request limit exceeded", http.StatusTooManyRequests)
		return "", false, false
	}
	softLimit := int64(30)
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OMI_MODEL_TIER")), "max") {
		softLimit = 300
	}
	if daily > softLimit && model == "gemini-2.5-pro" {
		path = strings.Replace(path, "gemini-2.5-pro", "gemini-2.5-flash-lite", 1)
	}
	return path, true, true
}

func (h Handler) Deepgram(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/proxy/deepgram")
	if path == "" || path == r.URL.Path {
		http.Error(w, "invalid Deepgram path", 400)
		return
	}
	h.forward(w, r, h.deepgramBase()+path, h.deepgramKey(), true)
}

func (h Handler) geminiRequestKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("X-LLM-BYOK-Key")); key != "" {
		return key
	}
	return h.geminiKey()
}

// WithTimeout returns a provider client with the same bounded logical request
// deadline used by the Python proxy. It is kept as a helper for wiring tests
// and for callers that need streaming-specific clients.
func WithTimeout(client *http.Client, timeout time.Duration) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	copy.Timeout = timeout
	return &copy
}
