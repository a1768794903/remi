package realtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"remi/server/internal/auth"
)

const (
	openAIModel = "gpt-realtime-2"
	geminiModel = "models/gemini-3.1-flash-live-preview"
)

type Handler struct {
	DB             *sql.DB
	OpenAIKey      string
	GeminiKey      string
	OpenAIEndpoint string
	GeminiEndpoint string
	Client         *http.Client
}

type mintRequest struct {
	Provider string `json:"provider"`
}
type usageRequest struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	TurnID      string `json:"turn_id"`
	InputText   int64  `json:"input_text_tokens"`
	InputAudio  int64  `json:"input_audio_tokens"`
	Cached      int64  `json:"input_cached_tokens"`
	OutputText  int64  `json:"output_text_tokens"`
	OutputAudio int64  `json:"output_audio_tokens"`
}
type usage struct{ InputText, InputAudio, CachedText, CachedAudio, OutputText, OutputAudio int64 }

func (h Handler) Mint(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		writeError(w, 401, "missing authenticated user", "auth_required", false)
		return
	}
	var in mintRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeError(w, 400, "invalid request body", "bad_request", false)
		return
	}
	in.Provider = strings.ToLower(strings.TrimSpace(in.Provider))
	if in.Provider != "openai" && in.Provider != "gemini" {
		writeError(w, 400, `provider must be "openai" or "gemini"`, "bad_provider", false)
		return
	}
	key, endpoint, model := h.provider(in.Provider)
	if key == "" {
		writeError(w, 503, fmt.Sprintf("%s realtime is not configured", strings.Title(in.Provider)), "provider_not_configured", true)
		return
	}
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	var data map[string]any
	if in.Provider == "openai" {
		data, err = postJSON(r.Context(), client, endpoint, map[string]string{"Authorization": "Bearer " + key}, map[string]any{"session": map[string]string{"type": "realtime", "model": model}}, nil)
	} else {
		now := time.Now().UTC()
		expires := now.Add(30 * time.Minute).Format("2006-01-02T15:04:05Z")
		start := now.Add(2 * time.Minute).Format("2006-01-02T15:04:05Z")
		data, err = postJSON(r.Context(), client, endpoint, nil, map[string]any{"uses": 1, "expireTime": expires, "newSessionExpireTime": start}, map[string]string{"key": key})
	}
	if err != nil {
		if upstream, ok := err.(upstreamError); ok {
			writeError(w, upstream.status, upstream.message, upstream.reason, upstream.retryable)
		} else {
			writeError(w, http.StatusBadGateway, err.Error(), "provider_mint_transport_error", true)
		}
		return
	}
	field := "value"
	if in.Provider == "gemini" {
		field = "name"
	}
	token, ok := data[field].(string)
	if !ok || token == "" {
		writeError(w, 502, "provider mint response did not contain a token", "provider_mint_transport_error", true)
		return
	}
	expires, _ := data["expires_at"].(string)
	if in.Provider == "gemini" {
		expires = time.Now().UTC().Add(30 * time.Minute).Format("2006-01-02T15:04:05Z")
	}
	if h.DB != nil {
		_, _ = h.DB.ExecContext(r.Context(), "INSERT INTO realtime_sessions (user_external_uid, token_hash, provider, model, status, expires_at, max_minutes, created_at) VALUES (?, ?, ?, ?, 'minted', ?, 30, UTC_TIMESTAMP(6))", uid, fmt.Sprintf("%x", sha256.Sum256([]byte(token))), in.Provider, model, expires)
	}
	out := map[string]any{"provider": in.Provider, "token": token}
	if expires != "" {
		out["expires_at"] = expires
	}
	writeJSON(w, 200, out)
}

func (h Handler) Usage(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		writeError(w, 401, "missing authenticated user", "auth_required", false)
		return
	}
	var in usageRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeError(w, 400, "invalid request body", "bad_request", false)
		return
	}
	if in.Provider != "openai" && in.Provider != "gemini" {
		writeError(w, 400, "provider is invalid", "bad_provider", false)
		return
	}
	u := usage{InputText: max0(in.InputText), InputAudio: max0(in.InputAudio), CachedText: min(max0(in.Cached), max0(in.InputText)), OutputText: max0(in.OutputText), OutputAudio: max0(in.OutputAudio)}
	u.CachedAudio = min(max0(in.Cached)-u.CachedText, u.InputAudio)
	total := u.InputText + u.InputAudio + u.OutputText + u.OutputAudio + u.CachedText + u.CachedAudio
	if total == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	model := in.Model
	if model == "" {
		_, _, model = h.provider(in.Provider)
	}
	cents := cost(in.Provider, model, u)
	if h.DB == nil {
		writeError(w, 503, "realtime usage storage is not configured", "usage_storage_unavailable", true)
		return
	}
	_, err = h.DB.ExecContext(r.Context(), "INSERT INTO realtime_usage (user_external_uid, turn_id, provider, model, input_tokens, output_tokens, cached_tokens, cost_micro_usd, created_at) VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE turn_id = turn_id", uid, in.TurnID, in.Provider, model, u.InputText+u.InputAudio, u.OutputText+u.OutputAudio, u.CachedText+u.CachedAudio, cents)
	if err != nil {
		writeError(w, 502, "realtime usage record failed", "usage_storage_error", true)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type upstreamError struct {
	status          int
	message, reason string
	retryable       bool
}

func (e upstreamError) Error() string { return e.message }
func postJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, body any, query map[string]string) (map[string]any, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, upstreamError{502, "provider mint transport error", "provider_mint_transport_error", true}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	if len(query) > 0 {
		q := req.URL.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, upstreamError{502, "provider mint transport error", "provider_mint_transport_error", true}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, classify(resp.StatusCode, string(data))
	}
	var out map[string]any
	if json.Unmarshal(data, &out) != nil {
		return nil, upstreamError{502, "provider mint response was not an object", "provider_mint_transport_error", true}
	}
	return out, nil
}
func classify(status int, body string) error {
	var x map[string]any
	_ = json.Unmarshal([]byte(body), &x)
	msg := body
	if e, ok := x["error"].(map[string]any); ok {
		if s, ok := e["message"].(string); ok {
			msg = s
		}
	}
	lower := strings.ToLower(msg)
	reason := "provider_mint_rejected"
	if status == 429 || strings.Contains(lower, "quota") {
		reason = "provider_quota_exceeded"
	}
	if status == 401 || status == 403 || strings.Contains(lower, "api key") || strings.Contains(lower, "authentication") {
		reason = "provider_auth_failed"
	}
	if status >= 500 {
		reason = "provider_mint_unavailable"
	}
	return upstreamError{status, msg, reason, status == 429 || status >= 500}
}
func (h Handler) provider(name string) (string, string, string) {
	if name == "openai" {
		return first(h.OpenAIKey, os.Getenv("OPENAI_API_KEY")), first(h.OpenAIEndpoint, os.Getenv("OPENAI_REALTIME_ENDPOINT"), "https://api.openai.com/v1/realtime/client_secrets"), openAIModel
	}
	return first(h.GeminiKey, os.Getenv("GEMINI_API_KEY")), first(h.GeminiEndpoint, os.Getenv("GEMINI_REALTIME_ENDPOINT"), "https://generativelanguage.googleapis.com/v1alpha/auth_tokens"), geminiModel
}
func first(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
func max0(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func cost(provider, model string, u usage) int64 {
	textIn, audioIn, textCache, audioCache, textOut, audioOut := int64(0), int64(0), u.CachedText, u.CachedAudio, u.OutputText, u.OutputAudio
	if provider == "openai" {
		textIn = 4_000_000
		audioIn = 32_000_000
		textOut = 24_000_000
		audioOut = 64_000_000
		textCache = 400_000
		audioCache = 400_000
	} else if strings.Contains(model, "2.5") {
		textIn = 500_000
		audioIn = 3_000_000
		textOut = 2_000_000
		audioOut = 12_000_000
		textCache = 500_000
		audioCache = 3_000_000
	} else {
		textIn = 750_000
		audioIn = 3_000_000
		textOut = 4_500_000
		audioOut = 12_000_000
		textCache = 3_000_000
		audioCache = 3_000_000
	}
	return ((u.InputText-u.CachedText)*textIn + u.CachedText*textCache + (u.InputAudio-u.CachedAudio)*audioIn + u.CachedAudio*audioCache + u.OutputText*textOut + u.OutputAudio*audioOut + 500_000) / 1_000_000
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, message, reason string, retryable bool) {
	writeJSON(w, status, map[string]any{"error": message, "reason": reason, "backend_route": "/v2/realtime/session", "retryable": retryable})
}
