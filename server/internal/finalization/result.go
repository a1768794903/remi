package finalization

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type TranscriptLine struct {
	Speaker string
	Text    string
}

type MemoryDraft struct {
	Type       string `json:"type"`
	Content    string `json:"content"`
	Category   string `json:"category"`
	Visibility string `json:"visibility"`
	Importance *int   `json:"importance"`
}

type ActionItemDraft struct {
	Description string `json:"description"`
	Owner       string `json:"owner"`
	Status      string `json:"status"`
}

type Result struct {
	Summary     string            `json:"summary"`
	Memories    []MemoryDraft     `json:"memories"`
	ActionItems []ActionItemDraft `json:"action_items"`
}

func ParseResult(raw string) (Result, error) {
	var result Result
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Result{}, fmt.Errorf("invalid finalization result: %w", err)
	}
	if strings.TrimSpace(result.Summary) == "" {
		return Result{}, errors.New("finalization result summary is required")
	}
	if len(result.Summary) > 100_000 || len(result.Memories) > 100 || len(result.ActionItems) > 100 {
		return Result{}, errors.New("finalization result exceeds limits")
	}
	for i := range result.Memories {
		result.Memories[i].Content = strings.TrimSpace(result.Memories[i].Content)
		if result.Memories[i].Content == "" || len(result.Memories[i].Content) > 8192 {
			return Result{}, errors.New("memory content is invalid")
		}
	}
	for i := range result.ActionItems {
		result.ActionItems[i].Description = strings.TrimSpace(result.ActionItems[i].Description)
		if result.ActionItems[i].Description == "" || len(result.ActionItems[i].Description) > 2000 {
			return Result{}, errors.New("action item description is invalid")
		}
	}
	return result, nil
}

func BuildTranscriptPrompt(lines []TranscriptLine) string {
	var builder strings.Builder
	builder.WriteString("Return JSON with exactly summary, memories, and action_items.\nTranscript:\n")
	for _, line := range lines {
		if strings.TrimSpace(line.Text) == "" {
			continue
		}
		builder.WriteString(line.Speaker)
		builder.WriteString(": ")
		builder.WriteString(line.Text)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func contains(value, fragment string) bool { return strings.Contains(value, fragment) }
