package memories

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type ExtractResult struct {
	Memories []string `json:"memories"`
	Profile  string   `json:"profile"`
}

func ParseExtractResult(raw string) (ExtractResult, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) >= 2 {
			raw = strings.Join(lines[1:], "\n")
			if i := strings.LastIndex(raw, "```"); i >= 0 {
				raw = raw[:i]
			}
		}
	}
	var result ExtractResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return ExtractResult{}, fmt.Errorf("invalid extraction JSON: %w", err)
	}
	if result.Memories == nil {
		result.Memories = []string{}
	}
	if len(result.Memories) > 100 {
		return ExtractResult{}, errors.New("extraction returned too many memories")
	}
	for i, value := range result.Memories {
		result.Memories[i] = strings.TrimSpace(value)
		if result.Memories[i] == "" {
			return ExtractResult{}, errors.New("extraction returned an empty memory")
		}
	}
	return result, nil
}
