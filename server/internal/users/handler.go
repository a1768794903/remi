package users

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"remi/server/internal/auth"
)

type Handler struct {
	Service Service
	Redis   *redis.Client
	HTTP    *http.Client
	DB      *sql.DB
}

func (h Handler) userJSONField(ctx context.Context, uid, key string) (any, error) {
	if h.DB == nil {
		return nil, errors.New("user storage is not configured")
	}
	var raw []byte
	if err := h.DB.QueryRowContext(ctx, `SELECT COALESCE(integrations, JSON_OBJECT()) FROM users WHERE external_uid=?`, uid).Scan(&raw); err != nil {
		return nil, err
	}
	var values map[string]any
	if len(raw) == 0 {
		values = map[string]any{}
	} else if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return values[key], nil
}

func (h Handler) setUserJSONField(ctx context.Context, uid, key string, value any) error {
	if h.DB == nil {
		return errors.New("user storage is not configured")
	}
	var raw []byte
	if err := h.DB.QueryRowContext(ctx, `SELECT COALESCE(integrations, JSON_OBJECT()) FROM users WHERE external_uid=?`, uid).Scan(&raw); err != nil {
		return err
	}
	values := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &values)
	}
	values[key] = value
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	_, err = h.DB.ExecContext(ctx, `UPDATE users SET integrations=?, updated_at=UTC_TIMESTAMP(6) WHERE external_uid=?`, encoded, uid)
	return err
}

func (h Handler) TrainingDataOptIn(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if r.Method == http.MethodPost {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Enabled == nil {
			http.Error(w, "enabled is required", 400)
			return
		}
		if err := h.setUserJSONField(r.Context(), uid, "training_data_opt_in", *input.Enabled); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	v, err := h.userJSONField(r.Context(), uid, "training_data_opt_in")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	enabled, _ := v.(bool)
	_ = json.NewEncoder(w).Encode(map[string]any{"enabled": enabled})
}

func (h Handler) LocationContextConsent(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if r.Method == http.MethodPut {
		var input struct {
			Enabled   *bool `json:"enabled"`
			Consented *bool `json:"consented"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		value := input.Enabled
		if value == nil {
			value = input.Consented
		}
		if value == nil {
			http.Error(w, "enabled is required", 400)
			return
		}
		if err := h.setUserJSONField(r.Context(), uid, "location_context_consent", *value); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	v, err := h.userJSONField(r.Context(), uid, "location_context_consent")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	consented, _ := v.(bool)
	_ = json.NewEncoder(w).Encode(map[string]any{"consented": consented, "enabled": consented})
}

func (h Handler) AppPreferences(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if r.Method == http.MethodPut {
		var input map[string]any
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if len(input) > 100 {
			http.Error(w, "too many preferences", 422)
			return
		}
		if err := h.setUserJSONField(r.Context(), uid, "app_preferences", input); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	v, err := h.userJSONField(r.Context(), uid, "app_preferences")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if v == nil {
		v = map[string]any{}
	}
	_ = json.NewEncoder(w).Encode(v)
}

func (h Handler) Export(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if h.DB == nil {
		http.Error(w, "user storage is not configured", 503)
		return
	}
	var email, name, language, timezone string
	var created, updated time.Time
	var raw []byte
	err = h.DB.QueryRowContext(r.Context(), `SELECT email,name,language,time_zone,created_at,updated_at,COALESCE(integrations,JSON_OBJECT()) FROM users WHERE external_uid=?`, uid).Scan(&email, &name, &language, &timezone, &created, &updated, &raw)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var settings map[string]any = map[string]any{}
	_ = json.Unmarshal(raw, &settings)
	_ = json.NewEncoder(w).Encode(map[string]any{"uid": uid, "email": email, "name": name, "language": language, "time_zone": timezone, "created_at": created, "updated_at": updated, "settings": settings, "conversations": []any{}, "memories": []any{}, "action_items": []any{}})
}

var byokFingerprintRE = regexp.MustCompile(`^[a-f0-9]{64}$`)
var allowedBYOKProviders = map[string]bool{"openai": true, "anthropic": true, "gemini": true, "openrouter": true, "deepgram": true}

func (h Handler) BYOKActive(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.DB == nil {
		http.Error(w, "user storage is not configured", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodDelete {
		if _, err = h.DB.ExecContext(r.Context(), `DELETE FROM user_byok WHERE user_external_uid=?`, uid); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"active": false})
		return
	}
	var input struct {
		Fingerprints map[string]string `json:"fingerprints"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if len(input.Fingerprints) == 0 {
		http.Error(w, "At least one LLM provider fingerprint is required", 400)
		return
	}
	llmProvider := false
	for provider, fingerprint := range input.Fingerprints {
		if !allowedBYOKProviders[provider] {
			http.Error(w, "Unknown provider: "+provider, 400)
			return
		}
		if provider != "deepgram" {
			llmProvider = true
		}
		if !byokFingerprintRE.MatchString(fingerprint) {
			http.Error(w, "Invalid fingerprint for "+provider+": expected lowercase hex SHA-256 (64 chars)", 400)
			return
		}
	}
	if !llmProvider {
		http.Error(w, "At least one LLM provider fingerprint is required", 400)
		return
	}
	raw, _ := json.Marshal(input.Fingerprints)
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO user_byok(user_external_uid,fingerprints,active,updated_at) VALUES(?,?,TRUE,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE fingerprints=VALUES(fingerprints),active=TRUE,updated_at=VALUES(updated_at)`, uid, raw)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"active": true})
}

const trialDuration = 3 * 24 * time.Hour

var trialFeatures = []string{"unlimited_listening", "unlimited_transcription", "unlimited_memories", "unlimited_insights", "30_chat_questions_per_month"}

func (h Handler) trialState(ctx context.Context, uid string) (map[string]any, error) {
	result := map[string]any{"trial_started_at": nil, "trial_ends_at": nil, "trial_remaining_seconds": 0, "trial_expired": false, "trial_duration_seconds": int64(trialDuration.Seconds()), "trial_features": trialFeatures, "plan_after_trial": "Free"}
	if strings.EqualFold(os.Getenv("TRIAL_PAYWALL_ENABLED"), "true") == false {
		return result, nil
	}
	var created time.Time
	if err := h.DB.QueryRowContext(ctx, `SELECT created_at FROM users WHERE external_uid=?`, uid).Scan(&created); err != nil {
		return result, err
	}
	var active int
	_ = h.DB.QueryRowContext(ctx, `SELECT active FROM user_byok WHERE user_external_uid=?`, uid).Scan(&active)
	if active == 1 {
		return result, nil
	}
	var plan, status string
	if err := h.DB.QueryRowContext(ctx, `SELECT plan,status FROM subscriptions WHERE user_external_uid=?`, uid).Scan(&plan, &status); err == nil && status != "canceled" && plan != "basic" {
		return result, nil
	}
	start := created.Unix()
	end := start + int64(trialDuration.Seconds())
	remaining := end - time.Now().UTC().Unix()
	if remaining < 0 {
		remaining = 0
	}
	result["trial_started_at"] = start
	result["trial_ends_at"] = end
	result["trial_remaining_seconds"] = remaining
	result["trial_expired"] = remaining == 0
	return result, nil
}

func (h Handler) Paywall(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	platform := r.Header.Get("X-App-Platform")
	if platform == "" {
		platform = r.URL.Query().Get("platform")
	}
	if h.DB == nil || (platform != "desktop" && platform != "macos" && platform != "windows") || !strings.EqualFold(os.Getenv("TRIAL_PAYWALL_ENABLED"), "true") {
		_ = json.NewEncoder(w).Encode(map[string]bool{"paywalled": false})
		return
	}
	state, err := h.trialState(r.Context(), uid)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]bool{"paywalled": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"paywalled": state["trial_expired"].(bool)})
}

// UsageQuota exposes the same monthly question-meter contract used by the
// desktop clients. The ledger is intentionally read-only here; chat/provider
// paths record usage independently so a quota read cannot consume allowance.
func (h Handler) UsageQuota(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.DB == nil {
		http.Error(w, "usage storage is not configured", http.StatusServiceUnavailable)
		return
	}
	plan := "basic"
	var status string
	_ = h.DB.QueryRowContext(r.Context(), `SELECT plan,status FROM subscriptions WHERE user_external_uid=?`, uid).Scan(&plan, &status)
	if status == "canceled" || strings.TrimSpace(plan) == "" {
		plan = "basic"
	}
	if active, queryErr := h.byokEnabled(r.Context(), uid); queryErr == nil && active && r.Header.Get("X-LLM-BYOK-Key") != "" {
		writeUsageQuota(w, map[string]any{"plan": "Free (BYOK)", "plan_type": "unlimited", "unit": "questions", "used": 0.0, "limit": nil, "percent": 0.0, "allowed": true, "reset_at": nil, "is_overage_plan": false})
		return
	}
	unit, limit := chatQuotaSpec(plan)
	var used float64
	monthStart := time.Now().UTC().Format("2006-01-02 15:04:05")
	query := `SELECT COALESCE(SUM(questions),0) FROM llm_usage WHERE user_external_uid=? AND created_at>=?`
	if unit == "cost_usd" {
		query = `SELECT COALESCE(SUM(cost_micro_usd),0) / 1000000.0 FROM llm_usage WHERE user_external_uid=? AND created_at>=?`
	}
	if err := h.DB.QueryRowContext(r.Context(), query, uid, monthStart[:8]+"01 00:00:00").Scan(&used); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	percent := chatQuotaPercent(used, limit)
	allowed := limit == nil || used < float64(*limit)
	nextMonth := time.Now().UTC().AddDate(0, 1, 0)
	resetAt := time.Date(nextMonth.Year(), nextMonth.Month(), 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	writeUsageQuota(w, map[string]any{"plan": chatPlanDisplayName(plan), "plan_type": plan, "unit": unit, "used": used, "limit": quotaLimitValue(limit), "percent": percent, "allowed": allowed, "reset_at": resetAt, "is_overage_plan": plan == "architect"})
}

func (h Handler) byokEnabled(ctx context.Context, uid string) (bool, error) {
	var enabled bool
	err := h.DB.QueryRowContext(ctx, `SELECT active FROM user_byok WHERE user_external_uid=?`, uid).Scan(&enabled)
	return enabled, err
}

func writeUsageQuota(w http.ResponseWriter, value map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func chatQuotaSpec(plan string) (string, *int64) {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case "architect":
		value := int64(400)
		return "cost_usd", &value
	case "operator":
		value := int64(500)
		return "questions", &value
	case "neo":
		value := int64(200)
		return "questions", &value
	default:
		value := int64(30)
		return "questions", &value
	}
}
func quotaLimitValue(value *int64) any {
	if value == nil {
		return nil
	}
	return float64(*value)
}
func chatQuotaPercent(used float64, limit *int64) float64 {
	if limit == nil || *limit <= 0 {
		return 0
	}
	percent := math.Round(10000*used/float64(*limit)) / 100
	if percent > 100 {
		return 100
	}
	if percent < 0 {
		return 0
	}
	return percent
}
func chatPlanDisplayName(plan string) string {
	switch strings.ToLower(plan) {
	case "operator":
		return "Operator"
	case "architect":
		return "Architect"
	case "neo":
		return "Neo"
	default:
		return "Free"
	}
}

func (h Handler) Trial(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if h.DB == nil {
		http.Error(w, "user storage is not configured", 503)
		return
	}
	state, err := h.trialState(r.Context(), uid)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"trial_remaining_seconds": 0, "trial_expired": false, "trial_duration_seconds": int64(trialDuration.Seconds()), "trial_features": trialFeatures, "plan_after_trial": "Free"})
		return
	}
	_ = json.NewEncoder(w).Encode(state)
}

var buttonEventLocks sync.Map

type geolocationInput struct {
	GooglePlaceID string   `json:"google_place_id,omitempty"`
	Latitude      float64  `json:"latitude"`
	Longitude     float64  `json:"longitude"`
	Address       string   `json:"address,omitempty"`
	LocationType  string   `json:"location_type,omitempty"`
	CapturedAt    *string  `json:"captured_at,omitempty"`
	CaptureSource string   `json:"capture_source,omitempty"`
	Accuracy      *float64 `json:"accuracy,omitempty"`
	Altitude      *float64 `json:"altitude,omitempty"`
}

func validGeolocation(g geolocationInput) bool {
	return !math.IsNaN(g.Latitude) && !math.IsInf(g.Latitude, 0) && g.Latitude >= -90 && g.Latitude <= 90 &&
		!math.IsNaN(g.Longitude) && !math.IsInf(g.Longitude, 0) && g.Longitude >= -180 && g.Longitude <= 180 &&
		(g.Accuracy == nil || (*g.Accuracy >= 0 && !math.IsNaN(*g.Accuracy) && !math.IsInf(*g.Accuracy, 0))) &&
		(g.CaptureSource == "" || g.CaptureSource == "current_position" || g.CaptureSource == "last_known_position" || g.CaptureSource == "manual" || g.CaptureSource == "integration")
}

func geoCacheKey(uid string) string { return "users:" + uid + ":geolocation" }

var developerWebhookTypes = map[string]bool{
	"audio_bytes": true, "audio_bytes_websocket": true, "realtime_transcript": true,
	"memory_created": true, "day_summary": true, "button_event": true,
}

func webhookKey(uid, kind string) string { return "users:" + uid + ":developer:webhook:" + kind }
func webhookStatusKey(uid, kind string) string {
	return "users:" + uid + ":developer:webhook_status:" + kind
}

func webhookHealthKey(uid, kind string) string { return "dev_webhook_health:" + uid + ":" + kind }

func webhookConfigured(kind, value string) bool {
	if kind == "audio_bytes" {
		value = strings.SplitN(value, ",", 2)[0]
	}
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func (h Handler) Profile(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPatch {
		var input UpdateInput
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		profile, err := h.Service.Update(r.Context(), uid, input)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(profile)
		return
	}
	profile, err := h.Service.Ensure(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(profile)
}

func (h Handler) UsageStats(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.UserID(r.Context()); err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	zero := map[string]any{"transcription_seconds": 0, "words_transcribed": 0, "insights_gained": 0, "memories_created": 0, "speech_seconds": 0}
	_ = json.NewEncoder(w).Encode(map[string]any{"today": zero, "monthly": zero, "yearly": zero, "all_time": zero, "history": []any{}})
}

func (h Handler) LLMUsage(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if h.DB == nil {
		http.Error(w, "usage storage is not configured", 503)
		return
	}
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		days, err = strconv.Atoi(raw)
		if err != nil || days < 1 || days > 365 {
			http.Error(w, "days must be between 1 and 365", 422)
			return
		}
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	rows, err := h.DB.QueryContext(r.Context(), `SELECT feature,COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(input_tokens+output_tokens),0),COUNT(*),COALESCE(SUM(cost_micro_usd),0) FROM llm_usage WHERE user_external_uid=? AND created_at>=? GROUP BY feature ORDER BY SUM(input_tokens+output_tokens) DESC`, uid, cutoff)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	summary := map[string]any{}
	top := []map[string]any{}
	for rows.Next() {
		var feature string
		var in, out, total, calls, cost int64
		if err := rows.Scan(&feature, &in, &out, &total, &calls, &cost); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		item := map[string]any{"feature": feature, "input_tokens": in, "output_tokens": out, "total_tokens": total, "call_count": calls}
		top = append(top, item)
		summary[feature] = map[string]any{"input_tokens": in, "output_tokens": out, "total_tokens": total, "call_count": calls, "cost_usd": float64(cost) / 1_000_000}
	}
	if len(top) > 5 {
		top = top[:5]
	}
	if r.Method == http.MethodPost {
		var in struct {
			InputTokens  int64    `json:"input_tokens"`
			OutputTokens int64    `json:"output_tokens"`
			TotalTokens  int64    `json:"total_tokens"`
			CostUSD      *float64 `json:"cost_usd"`
			Account      string   `json:"account"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if strings.TrimSpace(in.Account) == "" {
			in.Account = "omi"
		}
		cost := int64(0)
		if in.CostUSD != nil {
			cost = int64(*in.CostUSD*1_000_000 + 0.5)
		}
		_, err = h.DB.ExecContext(r.Context(), `INSERT INTO llm_usage(user_external_uid,allocation,feature,input_tokens,output_tokens,questions,cost_micro_usd,created_at) VALUES(?,?,?,?,?,?,?,UTC_TIMESTAMP(6))`, uid, "chat", in.Account, in.InputTokens, in.OutputTokens, 1, cost)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"summary": summary, "top_features": top, "period_days": days})
}

func (h Handler) LLMTopFeatures(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if h.DB == nil {
		http.Error(w, "usage storage is not configured", 503)
		return
	}
	days := 30
	limit := 3
	if raw := r.URL.Query().Get("days"); raw != "" {
		days, err = strconv.Atoi(raw)
		if err != nil || days < 1 || days > 365 {
			http.Error(w, "invalid days", 422)
			return
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 10 {
			http.Error(w, "invalid limit", 422)
			return
		}
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT feature,COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(input_tokens+output_tokens),0),COUNT(*) FROM llm_usage WHERE user_external_uid=? AND created_at>=? GROUP BY feature ORDER BY SUM(input_tokens+output_tokens) DESC LIMIT ?`, uid, time.Now().UTC().AddDate(0, 0, -days), limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var feature string
		var in, outTokens, total, calls int64
		if err := rows.Scan(&feature, &in, &outTokens, &total, &calls); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		out = append(out, map[string]any{"feature": feature, "input_tokens": in, "output_tokens": outTokens, "total_tokens": total, "call_count": calls})
	}
	_ = json.NewEncoder(w).Encode(out)
}
func (h Handler) LLMTotal(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if h.DB == nil {
		http.Error(w, "usage storage is not configured", 503)
		return
	}
	var cost int64
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(cost_micro_usd),0) FROM llm_usage WHERE user_external_uid=?`, uid).Scan(&cost); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]float64{"total_cost_usd": float64(cost) / 1_000_000})
}

func (h Handler) Geolocation(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var input geolocationInput
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if !validGeolocation(input) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Location ignored because its coordinates are invalid."})
		return
	}
	if h.Redis == nil {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	key := geoCacheKey(uid)
	var previous geolocationInput
	if raw, getErr := h.Redis.Get(r.Context(), key).Bytes(); getErr == nil {
		_ = json.Unmarshal(raw, &previous)
		if roundGeo(previous.Latitude) == roundGeo(input.Latitude) && roundGeo(previous.Longitude) == roundGeo(input.Longitude) {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Location not changed significantly."})
			return
		}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		http.Error(w, "failed to encode location", http.StatusInternalServerError)
		return
	}
	if err := h.Redis.Set(r.Context(), key, raw, 30*time.Minute).Err(); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h Handler) DeveloperWebhook(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	kind := r.PathValue("wtype")
	if !developerWebhookTypes[kind] {
		http.Error(w, "unknown webhook type", http.StatusBadRequest)
		return
	}
	if h.Redis == nil {
		http.Error(w, "redis is not configured", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	if r.Method == http.MethodGet {
		value, getErr := h.Redis.Get(ctx, webhookKey(uid, kind)).Result()
		if getErr == redis.Nil {
			value = ""
		} else if getErr != nil {
			http.Error(w, getErr.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"url": value})
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/disable") {
		if err := h.Redis.Set(ctx, webhookStatusKey(uid, kind), "false", 0).Err(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/enable") {
		if err := h.Redis.Set(ctx, webhookStatusKey(uid, kind), "true", 0).Err(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		URL string `json:"url"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.Redis.Set(ctx, webhookKey(uid, kind), input.URL, 0).Err(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	status := "false"
	if webhookConfigured(kind, input.URL) {
		status = "true"
	}
	if err := h.Redis.Set(ctx, webhookStatusKey(uid, kind), status, 0).Err(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h Handler) DeveloperWebhooksStatus(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if h.Redis == nil {
		http.Error(w, "redis is not configured", http.StatusServiceUnavailable)
		return
	}
	result := map[string]bool{}
	for _, kind := range []string{"audio_bytes", "memory_created", "realtime_transcript", "day_summary", "button_event"} {
		value, getErr := h.Redis.Get(r.Context(), webhookStatusKey(uid, kind)).Result()
		if getErr == redis.Nil {
			urlValue, urlErr := h.Redis.Get(r.Context(), webhookKey(uid, kind)).Result()
			if urlErr != nil && urlErr != redis.Nil {
				http.Error(w, urlErr.Error(), 500)
				return
			}
			result[kind] = webhookConfigured(kind, urlValue)
			_ = h.Redis.Set(r.Context(), webhookStatusKey(uid, kind), map[bool]string{true: "true", false: "false"}[result[kind]], 0).Err()
		} else if getErr != nil {
			http.Error(w, getErr.Error(), 500)
			return
		} else {
			result[kind] = value == "true"
		}
	}
	_ = json.NewEncoder(w).Encode(result)
}

func (h Handler) ButtonEvent(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.Redis == nil {
		http.Error(w, "redis is not configured", http.StatusServiceUnavailable)
		return
	}
	var input struct {
		ButtonEvent string  `json:"button_event"`
		DeviceID    string  `json:"device_id"`
		EventID     string  `json:"event_id"`
		Timestamp   string  `json:"timestamp"`
		SessionID   *string `json:"session_id"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if (input.ButtonEvent != "single_tap" && input.ButtonEvent != "double_tap" && input.ButtonEvent != "long_tap") || input.DeviceID == "" || len(input.DeviceID) > 128 || input.EventID == "" || input.Timestamp == "" {
		http.Error(w, "invalid button event", http.StatusBadRequest)
		return
	}
	if input.SessionID != nil && (len(*input.SessionID) == 0 || len(*input.SessionID) > 128) {
		http.Error(w, "invalid session_id", http.StatusBadRequest)
		return
	}
	mutexValue, _ := buttonEventLocks.LoadOrStore(uid+":"+input.DeviceID, &sync.Mutex{})
	mutex := mutexValue.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()
	if err := h.sendButtonEvent(r.Context(), uid, input); err != nil {
		// Delivery failures are deliberately not exposed as a client failure: Python
		// accepts the hardware event and records delivery health asynchronously.
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h Handler) sendButtonEvent(ctx context.Context, uid string, input struct {
	ButtonEvent string  `json:"button_event"`
	DeviceID    string  `json:"device_id"`
	EventID     string  `json:"event_id"`
	Timestamp   string  `json:"timestamp"`
	SessionID   *string `json:"session_id"`
}) error {
	status, err := h.Redis.Get(ctx, webhookStatusKey(uid, "button_event")).Result()
	if err != nil || status != "true" {
		return nil
	}
	endpoint, err := h.Redis.Get(ctx, webhookKey(uid, "button_event")).Result()
	if err != nil || !webhookConfigured("button_event", endpoint) {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	query := parsed.Query()
	query.Set("uid", uid)
	parsed.RawQuery = query.Encode()
	payload, _ := json.Marshal(map[string]any{"event_type": "button_event", "button_event": input.ButtonEvent, "device_id": input.DeviceID, "event_id": input.EventID, "timestamp": input.Timestamp, "session_id": input.SessionID})
	client := h.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	delays := webhookRetryDelays()
	attempts := len(delays) + 1
	var lastStatus int
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(payload))
		if requestErr != nil {
			lastErr = requestErr
			break
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", input.EventID)
		response, requestErr := client.Do(req)
		if requestErr == nil {
			lastStatus = response.StatusCode
			response.Body.Close()
			if lastStatus >= 200 && lastStatus < 300 {
				h.recordWebhookSuccess(ctx, uid, "button_event")
				return nil
			}
			if lastStatus >= 400 && lastStatus < 500 && lastStatus != 408 && lastStatus != 429 {
				break
			}
		} else {
			lastErr = requestErr
		}
		if attempt < len(delays) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delays[attempt]):
			}
		}
	}
	if lastStatus != 0 {
		h.recordWebhookFailure(ctx, uid, "button_event", lastStatus)
	}
	return lastErr
}

func webhookRetryDelays() []time.Duration {
	raw := os.Getenv("DEV_WEBHOOK_RETRY_DELAYS")
	if raw == "" {
		return []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}
	}
	var result []time.Duration
	for _, part := range strings.Split(raw, ",") {
		value, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}
		}
		result = append(result, time.Duration(value*float64(time.Second)))
	}
	return result
}

func (h Handler) recordWebhookSuccess(ctx context.Context, uid, kind string) {
	key := webhookHealthKey(uid, kind)
	_ = h.Redis.HSet(ctx, key, "failure_count", 0, "disabled", 0).Err()
	_ = h.Redis.Expire(ctx, key, 24*time.Hour).Err()
}
func (h Handler) recordWebhookFailure(ctx context.Context, uid, kind string, status int) {
	key := webhookHealthKey(uid, kind)
	count, _ := h.Redis.HIncrBy(ctx, key, "failure_count", 1).Result()
	_ = h.Redis.HSet(ctx, key, "last_status", status).Err()
	_ = h.Redis.Expire(ctx, key, 24*time.Hour).Err()
	if count >= 100 {
		_ = h.Redis.Set(ctx, webhookStatusKey(uid, kind), "false", 0).Err()
		_ = h.Redis.HSet(ctx, key, "disabled", 1).Err()
	}
}

func roundGeo(value float64) float64 { return math.Round(value*10000) / 10000 }
func (h Handler) Language(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPatch {
		var input struct {
			Language string `json:"language"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		profile, err := h.Service.Update(r.Context(), uid, UpdateInput{Language: &input.Language})
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "language": profile.Language})
		return
	}
	profile, err := h.Service.Ensure(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"language": profile.Language})
}

func (h Handler) AvailableLanguages(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{"languages": []map[string]string{
		{"code": "en", "name": "English"}, {"code": "es", "name": "Spanish"}, {"code": "fr", "name": "French"}, {"code": "de", "name": "German"}, {"code": "it", "name": "Italian"}, {"code": "pt", "name": "Portuguese"}, {"code": "ja", "name": "Japanese"}, {"code": "ko", "name": "Korean"}, {"code": "zh-CN", "name": "Chinese (Simplified)"}, {"code": "zh-TW", "name": "Chinese (Traditional)"}, {"code": "vi", "name": "Vietnamese"},
	}})
}

func (h Handler) TranscriptionPreferences(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if r.Method == http.MethodPatch {
		var in struct {
			SingleLanguageMode *bool     `json:"single_language_mode"`
			Vocabulary         *[]string `json:"vocabulary"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if err = h.Service.UpdateTranscriptionPreferences(r.Context(), uid, in.SingleLanguageMode, in.Vocabulary); errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	p, err := h.Service.TranscriptionPreferences(r.Context(), uid)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(p)
}

func (h Handler) NotificationSettings(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPatch {
		var input struct {
			Enabled   *bool `json:"enabled"`
			Frequency *int  `json:"frequency"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		settings, err := h.Service.UpdateNotificationSettings(r.Context(), uid, input.Enabled, input.Frequency)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(settings)
		return
	}
	settings, err := h.Service.NotificationSettings(r.Context(), uid)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(settings)
}

func (h Handler) AssistantSettings(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPatch {
		var patch map[string]any
		if json.NewDecoder(r.Body).Decode(&patch) != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		value, err := h.Service.UpdateAssistantSettings(r.Context(), uid, patch)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(value)
		return
	}
	value, err := h.Service.AssistantSettings(r.Context(), uid)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func (h Handler) AIProfile(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPatch {
		var input AIProfile
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		value, err := h.Service.UpdateAIProfile(r.Context(), uid, input)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(value)
		return
	}
	value, exists, err := h.Service.AIProfile(r.Context(), uid)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		_ = json.NewEncoder(w).Encode(nil)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func (h Handler) Onboarding(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPatch {
		var input map[string]any
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if err := h.Service.UpdateOnboarding(r.Context(), uid, input); errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	state, err := h.Service.Onboarding(r.Context(), uid)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(state)
}

func (h Handler) PrivateCloudSync(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPost {
		raw, readErr := io.ReadAll(io.LimitReader(r.Body, 1024))
		if readErr != nil {
			http.Error(w, "invalid JSON boolean", http.StatusBadRequest)
			return
		}
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			var wrapped struct {
				Value *bool `json:"value"`
			}
			if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Value == nil {
				http.Error(w, "invalid JSON boolean", http.StatusBadRequest)
				return
			}
			value = *wrapped.Value
		}
		if err := h.Service.SetPrivateCloudSync(r.Context(), uid, value); errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	state, err := h.Service.PrivateCloudSync(r.Context(), uid)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(state)
}

func (h Handler) ScreenFrameSettings(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPatch {
		var input struct {
			Enabled *bool `json:"meeting_note_screenshots_enabled"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Enabled == nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		value, err := h.Service.SetScreenFrameSettings(r.Context(), uid, *input.Enabled)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(value)
		return
	}
	value, err := h.Service.ScreenFrameSettings(r.Context(), uid)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func (h Handler) StoreRecordingPermission(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodPost {
		raw, readErr := io.ReadAll(io.LimitReader(r.Body, 1024))
		if readErr != nil {
			http.Error(w, "invalid JSON boolean", http.StatusBadRequest)
			return
		}
		var enabled bool
		if json.Unmarshal(raw, &enabled) != nil {
			var input struct {
				Enabled *bool `json:"value"`
			}
			if err := json.Unmarshal(raw, &input); err != nil || input.Enabled == nil {
				http.Error(w, "invalid JSON boolean", http.StatusBadRequest)
				return
			}
			enabled = *input.Enabled
		}
		if err := h.Service.SetStoreRecordingPermission(r.Context(), uid, enabled); errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	if r.Method == http.MethodDelete {
		if err := h.Service.SetStoreRecordingPermission(r.Context(), uid, false); errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	value, err := h.Service.StoreRecordingPermission(r.Context(), uid)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}
