package automodel

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	qualityWeight = 0.65
	speedWeight   = 0.35
	speedCap      = 250.0
	cacheTTL      = 24 * time.Hour
)

type modelMetrics struct {
	Quality float64
	Speed   float64
}
type Result struct {
	Provider    string         `json:"provider"`
	UpdatedAt   float64        `json:"updated_at"`
	Detail      map[string]any `json:"detail"`
	Attribution string         `json:"attribution"`
}
type Service struct {
	mu       sync.Mutex
	provider string
	updated  time.Time
	detail   map[string]any
	client   *http.Client
}

func score(quality, speed float64) float64 {
	if quality < 0 {
		quality = 0
	}
	if quality > 100 {
		quality = 100
	}
	if speed < 0 {
		speed = 0
	}
	if speed > speedCap {
		speed = speedCap
	}
	return qualityWeight*quality/100 + speedWeight*speed/speedCap
}
func pick(models map[string]modelMetrics) (string, map[string]any) {
	scores := map[string]float64{}
	for k, v := range models {
		scores[k] = round(score(v.Quality, v.Speed), 4)
	}
	if len(scores) == 0 {
		return "geminiFlashLive", map[string]any{"reason": "no matching AA models", "scores": map[string]float64{}}
	}
	best := "geminiFlashLive"
	for k, v := range scores {
		if _, ok := scores[best]; !ok || v > scores[best] {
			best = k
		}
	}
	return best, map[string]any{"scores": scores}
}
func round(v float64, digits int) float64 {
	p := 1.0
	for i := 0; i < digits; i++ {
		p *= 10
	}
	n := float64(int(v*p + 0.5))
	return n / p
}

func (s *Service) Current(ctx context.Context) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider == "" || time.Since(s.updated) > cacheTTL {
		s.refreshLocked(ctx)
	}
	if s.provider == "" {
		s.provider = "geminiFlashLive"
		s.updated = time.Now()
		s.detail = map[string]any{"reason": "model selection unavailable"}
	}
	return Result{Provider: s.provider, UpdatedAt: float64(s.updated.UnixNano()) / 1e9, Detail: s.detail, Attribution: "https://artificialanalysis.ai/"}
}
func (s *Service) refreshLocked(ctx context.Context) {
	key := os.Getenv("ARTIFICIALANALYSIS_API_KEY")
	if key == "" {
		s.provider = "geminiFlashLive"
		s.updated = time.Now()
		s.detail = map[string]any{"reason": "no ARTIFICIALANALYSIS_API_KEY; default to Gemini"}
		return
	}
	client := s.client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://artificialanalysis.ai/api/v2/data/llms/models", nil)
	req.Header.Set("x-api-key", key)
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}
	var payload struct {
		Data []struct {
			Slug        string `json:"slug"`
			ID          string `json:"id"`
			Name        string `json:"name"`
			Evaluations struct {
				Quality float64 `json:"artificial_analysis_intelligence_index"`
			} `json:"evaluations"`
			Speed float64 `json:"median_output_tokens_per_second"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&payload) != nil {
		return
	}
	metrics := map[string]modelMetrics{}
	for _, m := range payload.Data {
		slug := strings.ToLower(m.Slug + " " + m.ID + " " + m.Name)
		if strings.Contains(slug, "gemini-3-5-flash") {
			metrics["geminiFlashLive"] = modelMetrics{m.Evaluations.Quality, m.Speed}
		}
		if strings.Contains(slug, "gpt-5") {
			metrics["gptRealtime2"] = modelMetrics{m.Evaluations.Quality, m.Speed}
		}
	}
	p, d := pick(metrics)
	s.provider = p
	s.detail = d
	s.updated = time.Now()
}

type Handler struct{ Service *Service }

func (h Handler) Pick(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		http.Error(w, "model selection unavailable", 503)
		return
	}
	_ = json.NewEncoder(w).Encode(h.Service.Current(r.Context()))
}
