package users

import (
	"context"
	"testing"

	"remi/server/internal/chat"
)

type synthesisProvider struct {
	answer string
}

func (p synthesisProvider) Complete(context.Context, []chat.Turn) (string, error) {
	return p.answer, nil
}

func TestSynthesizeAIProfileReturnsStructuredProviderResult(t *testing.T) {
	got, err := synthesizeAIProfile(context.Background(), synthesisProvider{answer: `{"profile_text":"Profile","data_sources_used":["memories"],"item_count":2}`}, SynthesizeAIProfileRequest{Memories: []string{"one", "two"}})
	if err != nil || got.ProfileText != "Profile" || got.ItemCount != 2 {
		t.Fatalf("result=%+v err=%v", got, err)
	}
}
