package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type Provider interface {
	Complete(context.Context, []Turn) (string, error)
}
type CompletionUsage struct {
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	CostMicroUSD int64
}
type UsageCompleter interface {
	CompleteWithUsage(context.Context, []Turn) (string, CompletionUsage, error)
}
type NullProvider struct{}

func (NullProvider) Complete(context.Context, []Turn) (string, error) {
	return "", errors.New("chat provider is not configured")
}

type HTTPProvider struct {
	Endpoint string
	APIKey   string
	Model    string
	Client   *http.Client
}

func (p HTTPProvider) Complete(ctx context.Context, turns []Turn) (string, error) {
	answer, _, err := p.CompleteWithUsage(ctx, turns)
	return answer, err
}

func (p HTTPProvider) CompleteWithUsage(ctx context.Context, turns []Turn) (string, CompletionUsage, error) {
	if strings.TrimSpace(p.Endpoint) == "" {
		return "", CompletionUsage{}, errors.New("chat endpoint is not configured")
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	payload := struct {
		Model    string `json:"model"`
		Messages []Turn `json:"messages"`
		Stream   bool   `json:"stream"`
	}{Model: p.Model, Messages: turns, Stream: false}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", CompletionUsage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", CompletionUsage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", CompletionUsage{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", CompletionUsage{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", CompletionUsage{}, fmt.Errorf("chat provider returned %s: %s", resp.Status, string(raw))
	}
	var result struct {
		Choices []struct {
			Message Turn `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			InputTokens      int64 `json:"input_tokens"`
			OutputTokens     int64 `json:"output_tokens"`
			CachedTokens     int64 `json:"cached_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", CompletionUsage{}, err
	}
	if len(result.Choices) == 0 || strings.TrimSpace(result.Choices[0].Message.Content) == "" {
		return "", CompletionUsage{}, errors.New("chat provider returned no answer")
	}
	in := result.Usage.InputTokens
	if in == 0 {
		in = result.Usage.PromptTokens
	}
	out := result.Usage.OutputTokens
	if out == 0 {
		out = result.Usage.CompletionTokens
	}
	cached := result.Usage.CachedTokens
	return result.Choices[0].Message.Content, CompletionUsage{InputTokens: in, OutputTokens: out, CachedTokens: cached, CostMicroUSD: estimateCostMicroUSD(p.Model, in, out, cached)}, nil
}

func estimateCostMicroUSD(model string, input, output, cached int64) int64 {
	// Prices are USD per million tokens; keep this table intentionally conservative
	// for the OpenAI-compatible models used by the Go service.
	model = strings.ToLower(model)
	inputRate, outputRate := 0.15, 0.60
	if strings.Contains(model, "gpt-4o") && !strings.Contains(model, "mini") {
		inputRate, outputRate = 2.50, 10.00
	}
	if strings.Contains(model, "claude-3-5-sonnet") {
		inputRate, outputRate = 3.00, 15.00
	}
	if strings.Contains(model, "gemini") {
		inputRate, outputRate = 0.30, 2.50
	}
	if input < cached {
		cached = input
	}
	cost := (float64(input-cached)*inputRate + float64(cached)*inputRate*0.25 + float64(output)*outputRate) / 1_000_000 * 1_000_000
	return int64(cost + 0.5)
}
