package proxy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
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
	DB           *sql.DB
}

type usageCounts struct {
	Input  int64
	Output int64
	Cached int64
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

func proxyRequestID(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Omi-Request-Id")); value != "" && len(value) <= 128 {
		return value
	}
	return uuid.NewString()
}

func (h Handler) recordAttempt(ctx context.Context, requestID string, retryOrdinal int, uid, provider, model, action, payer string, status int, outcome string, inputBytes, inputTokens, outputTokens, cachedTokens int64) {
	if h.DB == nil {
		return
	}
	_, _ = h.DB.ExecContext(ctx, `INSERT INTO llm_proxy_attempts(request_id,retry_ordinal,user_external_uid,caller,provider,model,api_surface,payer,outcome,upstream_status,input_bytes,input_tokens,output_tokens,cached_tokens,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE request_id=request_id,retry_ordinal=retry_ordinal`, requestID, retryOrdinal, nullString(uid), "desktop_proxy", provider, model, action, payer, outcome, status, inputBytes, inputTokens, outputTokens, cachedTokens)
}

func (h Handler) updateAttemptUsage(ctx context.Context, requestID string, retryOrdinal int, usage usageCounts) {
	if h.DB == nil || requestID == "" {
		return
	}
	_, _ = h.DB.ExecContext(ctx, `UPDATE llm_proxy_attempts SET input_tokens=?,output_tokens=?,cached_tokens=? WHERE request_id=? AND retry_ordinal=?`, usage.Input, usage.Output, usage.Cached, requestID, retryOrdinal)
}

func (h Handler) updateAttemptOutcome(ctx context.Context, requestID string, retryOrdinal int, outcome string) {
	if h.DB == nil || requestID == "" || outcome == "" {
		return
	}
	_, _ = h.DB.ExecContext(ctx, `UPDATE llm_proxy_attempts SET outcome=? WHERE request_id=? AND retry_ordinal=?`, outcome, requestID, retryOrdinal)
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func (h Handler) forward(w http.ResponseWriter, r *http.Request, target string, key string, bearer bool, provider, model, action, uid, payer string, streaming bool, fallback []string) {
	requestID := proxyRequestID(r)
	body, readErr := io.ReadAll(io.LimitReader(r.Body, maxGeminiBodyBytes+1))
	if readErr != nil || int64(len(body)) > maxGeminiBodyBytes {
		http.Error(w, "provider request body could not be buffered", http.StatusRequestEntityTooLarge)
		return
	}
	targets := append([]string{target}, fallback...)
	var resp *http.Response
	var err error
	selectedOrdinal := 0
	for index, candidate := range targets {
		retryOrdinal := index
		selectedOrdinal = index
		u, parseErr := url.Parse(candidate)
		if parseErr != nil {
			h.recordAttempt(r.Context(), requestID, retryOrdinal, uid, provider, model, action, payer, 0, "validation_rejected", int64(len(body)), 0, 0, 0)
			http.Error(w, "invalid provider endpoint", 500)
			return
		}
		u.RawQuery = r.URL.RawQuery
		req, requestErr := http.NewRequestWithContext(r.Context(), r.Method, u.String(), bytes.NewReader(body))
		if requestErr != nil {
			err = requestErr
			break
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
		resp, err = h.client().Do(req)
		var candidateBody []byte
		if err == nil && !streaming {
			candidateBody, _ = io.ReadAll(io.LimitReader(resp.Body, maxGeminiBodyBytes*2))
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(candidateBody))
		}
		if err == nil && (!fallbackEligible(resp.StatusCode, candidateBody, streaming) || index == len(targets)-1) {
			selectedOrdinal = index
			break
		}
		if resp != nil {
			_, code, _, _ := proxyStatus(resp.StatusCode)
			outcome := code
			if outcome == "" {
				outcome = "provider_error"
			}
			h.recordAttempt(r.Context(), requestID, retryOrdinal, uid, provider, model, action, payer, resp.StatusCode, outcome, int64(len(body)), 0, 0, 0)
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}
	if err != nil || resp == nil {
		h.recordAttempt(r.Context(), requestID, selectedOrdinal, uid, provider, model, action, payer, 0, "provider_unavailable", int64(len(body)), 0, 0, 0)
		w.Header().Set("X-Omi-Request-Id", requestID)
		w.Header().Set("X-Omi-Provider", provider)
		w.Header().Set("X-Omi-Error-Class", "provider_unavailable")
		w.Header().Set("X-Omi-Failure-Phase", "provider")
		w.Header().Set("X-Omi-Retryable", "true")
		w.Header().Set("Retry-After", "10")
		writeProxyError(w, http.StatusServiceUnavailable, "provider_unavailable", "provider is temporarily unavailable", requestID, true)
		return
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.Header().Set("X-Omi-Request-Id", requestID)
	w.Header().Set("X-Omi-Provider", provider)
	status, code, retryable, retryAfter := proxyStatus(resp.StatusCode)
	outcome := "success"
	if code != "" {
		outcome = code
	}
	var responseBody []byte
	if !streaming {
		responseBody, _ = io.ReadAll(io.LimitReader(resp.Body, maxGeminiBodyBytes*2))
	}
	usage := parseUsage(responseBody)
	if !streaming && resp.StatusCode >= 200 && resp.StatusCode < 300 && (action == "generateContent" || action == "streamGenerateContent") {
		hasContent, hasTerminal, hasError := geminiPayloadOutcome(responseBody)
		switch {
		case hasError:
			outcome = "provider_error"
		case !hasContent:
			outcome = "empty_answer"
		case !hasTerminal:
			outcome = "missing_terminal"
		}
	}
	finalOrdinal := selectedOrdinal
	h.recordAttempt(r.Context(), requestID, finalOrdinal, uid, provider, model, action, payer, resp.StatusCode, outcome, int64(len(body)), usage.Input, usage.Output, usage.Cached)
	if code != "" {
		w.Header().Set("X-Omi-Error-Class", code)
		w.Header().Set("X-Omi-Failure-Phase", "provider")
		w.Header().Set("X-Omi-Retryable", boolString(retryable))
		if retryAfter > 0 {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		}
	}
	w.WriteHeader(status)
	if !streaming {
		_, _ = w.Write(responseBody)
		return
	}
	observer := &usageReader{source: resp.Body, limit: maxGeminiBodyBytes * 2, usage: func(value usageCounts) { h.updateAttemptUsage(r.Context(), requestID, finalOrdinal, value) }, terminal: func(body []byte) {
		_, terminal, hasError := geminiPayloadOutcome(body)
		outcome := "empty_answer"
		if hasError {
			outcome = "provider_error"
		} else if terminal {
			outcome = "success"
		} else {
			outcome = "missing_terminal"
		}
		h.updateAttemptOutcome(r.Context(), requestID, finalOrdinal, outcome)
	}}
	_, _ = io.Copy(w, observer)
}

func fallbackEligible(status int, body []byte, streaming bool) bool {
	if streaming {
		// A stream must remain untouched until it is copied to the client. The
		// status-only route is retained for stream setup failures; once bytes
		// have started, the terminal classifier owns the outcome.
		return status == http.StatusNotFound || status >= 500
	}
	switch classifyProviderFailure(status, body) {
	case "model_unavailable", "capacity_exhausted", "capacity_absent":
		return true
	default:
		return false
	}
}

func classifyProviderFailure(status int, body []byte) string {
	text := strings.ToLower(string(body))
	if status == http.StatusNotFound && strings.Contains(text, "publisher model") {
		return "model_unavailable"
	}
	if status == http.StatusTooManyRequests &&
		(strings.Contains(text, "provisioned throughput") || strings.Contains(text, "dedicated")) {
		return "capacity_exhausted"
	}
	if status == http.StatusBadRequest || status == http.StatusForbidden || status == http.StatusNotFound || status == http.StatusTooManyRequests {
		if (strings.Contains(text, "provisioned throughput") || strings.Contains(text, "dedicated")) &&
			(strings.Contains(text, "not found") || strings.Contains(text, "no provisioned") || strings.Contains(text, "does not exist") || strings.Contains(text, "not configured")) {
			return "capacity_absent"
		}
	}
	if status >= 500 {
		return "provider_error"
	}
	if status >= 400 {
		return "provider_rejected"
	}
	return ""
}

func (h Handler) Gemini(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/proxy/gemini")
	if path == "" || path == r.URL.Path {
		http.Error(w, "invalid Gemini path", 400)
		return
	}
	model, action, err := geminiPath(path)
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
	uid, _ := auth.UserID(r.Context())
	payer := "omi"
	if strings.TrimSpace(r.Header.Get("X-LLM-BYOK-Key")) != "" {
		payer = "byok"
	}
	h.forward(w, r, h.geminiBase()+path, h.geminiRequestKey(r), false, "gemini", model, action, uid, payer, false, geminiFallbackPaths(h.geminiBase(), path, model, action))
}

func (h Handler) GeminiStream(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/proxy/gemini-stream")
	if path == "" || path == r.URL.Path {
		http.Error(w, "invalid Gemini path", 400)
		return
	}
	model, action, err := geminiPath(path)
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
	uid, _ := auth.UserID(r.Context())
	payer := "omi"
	if strings.TrimSpace(r.Header.Get("X-LLM-BYOK-Key")) != "" {
		payer = "byok"
	}
	h.forward(w, r, h.geminiBase()+path, h.geminiRequestKey(r), false, "gemini", model, action, uid, payer, true, geminiFallbackPaths(h.geminiBase(), path, model, action))
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
	uid, _ := auth.UserID(r.Context())
	h.forward(w, r, h.deepgramBase()+path, h.deepgramKey(), true, "deepgram", "deepgram", "listen", uid, "omi", false, nil)
}

func geminiFallbackPaths(base, path, model, action string) []string {
	if action != "generateContent" && action != "streamGenerateContent" {
		return nil
	}
	chain := map[string][]string{
		"gemini-2.5-pro":        {"gemini-2.5-flash-lite", "gemini-2.5-flash"},
		"gemini-2.5-flash-lite": {"gemini-2.5-flash"},
	}
	models := chain[model]
	result := make([]string, 0, len(models))
	for _, next := range models {
		result = append(result, base+strings.Replace(path, model+":"+action, next+":"+action, 1))
	}
	return result
}

type usageReader struct {
	source   io.Reader
	buffer   bytes.Buffer
	limit    int64
	usage    func(usageCounts)
	terminal func([]byte)
	done     bool
}

func (r *usageReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if n > 0 && int64(r.buffer.Len()) < r.limit {
		remaining := r.limit - int64(r.buffer.Len())
		copyCount := n
		if int64(copyCount) > remaining {
			copyCount = int(remaining)
		}
		_, _ = r.buffer.Write(p[:copyCount])
	}
	if err == io.EOF && !r.done {
		r.done = true
		if r.usage != nil {
			r.usage(parseUsage(r.buffer.Bytes()))
		}
		if r.terminal != nil {
			r.terminal(r.buffer.Bytes())
		}
	}
	return n, err
}

func geminiPayloadOutcome(body []byte) (hasContent, hasTerminal, hasError bool) {
	inspect := func(value any) {}
	var walk func(any)
	walk = func(value any) {
		switch item := value.(type) {
		case []any:
			for _, child := range item {
				walk(child)
			}
		case map[string]any:
			if _, ok := item["error"]; ok {
				hasError = true
			}
			if reason, ok := item["finishReason"].(string); ok && strings.TrimSpace(reason) != "" {
				hasTerminal = true
			}
			if reason, ok := item["finish_reason"].(string); ok && strings.TrimSpace(reason) != "" {
				hasTerminal = true
			}
			if content, ok := item["content"].(map[string]any); ok {
				walk(content)
			}
			if parts, ok := item["parts"].([]any); ok {
				for _, part := range parts {
					if object, ok := part.(map[string]any); ok {
						if text, ok := object["text"].(string); ok && strings.TrimSpace(text) != "" {
							hasContent = true
						}
						walk(object)
					}
				}
			}
			for key, child := range item {
				if key != "content" && key != "parts" {
					walk(child)
				}
			}
		}
	}
	_ = inspect
	var root any
	if json.Unmarshal(body, &root) == nil {
		walk(root)
		return
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if line == "" {
			continue
		}
		var value any
		if json.Unmarshal([]byte(line), &value) == nil {
			walk(value)
		}
	}
	return
}

func parseUsage(body []byte) usageCounts {
	if len(body) == 0 {
		return usageCounts{}
	}
	var root any
	if json.Unmarshal(body, &root) != nil {
		// Streaming responses may be newline-delimited or prefixed with data:.
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var candidate any
			if line != "" && json.Unmarshal([]byte(line), &candidate) == nil {
				if usage := usageFromJSON(candidate); usage != (usageCounts{}) {
					root = candidate
				}
			}
		}
	}
	return usageFromJSON(root)
}

func usageFromJSON(value any) usageCounts {
	object, ok := value.(map[string]any)
	if !ok {
		if list, ok := value.([]any); ok {
			for i := len(list) - 1; i >= 0; i-- {
				if usage := usageFromJSON(list[i]); usage != (usageCounts{}) {
					return usage
				}
			}
		}
		return usageCounts{}
	}
	metadata, _ := object["usageMetadata"].(map[string]any)
	if metadata == nil {
		metadata, _ = object["usage_metadata"].(map[string]any)
	}
	if metadata == nil {
		for _, item := range object {
			if usage := usageFromJSON(item); usage != (usageCounts{}) {
				return usage
			}
		}
		return usageCounts{}
	}
	number := func(key string) int64 {
		value, _ := metadata[key].(float64)
		return int64(value)
	}
	return usageCounts{Input: number("promptTokenCount"), Output: number("candidatesTokenCount"), Cached: number("cachedContentTokenCount")}
}

func proxyStatus(upstream int) (status int, code string, retryable bool, retryAfter int) {
	switch {
	case upstream == http.StatusTooManyRequests:
		return http.StatusTooManyRequests, "provider_rate_limited", true, 30
	case upstream == http.StatusRequestTimeout || upstream == http.StatusGatewayTimeout:
		return http.StatusGatewayTimeout, "provider_timeout", false, 0
	case upstream == http.StatusBadGateway || upstream == http.StatusServiceUnavailable:
		return http.StatusServiceUnavailable, "provider_unavailable", true, 10
	case upstream >= 500:
		return http.StatusBadGateway, "provider_unavailable", false, 0
	case upstream >= 400:
		return upstream, "provider_rejected", false, 0
	default:
		return upstream, "", false, 0
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func writeProxyError(w http.ResponseWriter, status int, code, message, requestID string, retryable bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": code, "message": message, "request_id": requestID, "retryable": retryable})
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
