package users

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"remi/server/internal/auth"
	"remi/server/internal/chat"
)

type SynthesizeAIProfileRequest struct {
	Memories      []string `json:"memories"`
	Tasks         []string `json:"tasks"`
	Goals         []string `json:"goals"`
	Conversations []string `json:"conversations"`
	Messages      []string `json:"messages"`
	PastProfiles  []string `json:"past_profiles"`
}

type SynthesizeAIProfileResponse struct {
	ProfileText     string   `json:"profile_text"`
	DataSourcesUsed []string `json:"data_sources_used"`
	ItemCount       int      `json:"item_count"`
}

func validateProfileSources(in SynthesizeAIProfileRequest) error {
	for _, values := range [][]string{in.Memories, in.Tasks, in.Goals, in.Conversations, in.Messages} {
		if len(values) > 500 {
			return errors.New("each profile source accepts at most 500 items")
		}
	}
	if len(in.PastProfiles) > 5 {
		return errors.New("past_profiles accepts at most 5 items")
	}
	return nil
}

func synthesizeAIProfile(ctx context.Context, provider chat.Provider, in SynthesizeAIProfileRequest) (SynthesizeAIProfileResponse, error) {
	if err := validateProfileSources(in); err != nil {
		return SynthesizeAIProfileResponse{}, err
	}
	if provider == nil {
		return SynthesizeAIProfileResponse{}, errors.New("ai profile provider is not configured")
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return SynthesizeAIProfileResponse{}, err
	}
	answer, err := provider.Complete(ctx, []chat.Turn{
		{Role: "system", Content: "Synthesize a concise user profile from the supplied source lines. Return only JSON with profile_text (string), data_sources_used (array of strings), and item_count (integer). Do not invent facts."},
		{Role: "user", Content: string(payload)},
	})
	if err != nil {
		return SynthesizeAIProfileResponse{}, err
	}
	var out SynthesizeAIProfileResponse
	if err := json.Unmarshal([]byte(answer), &out); err != nil || strings.TrimSpace(out.ProfileText) == "" || out.ItemCount < 0 {
		return SynthesizeAIProfileResponse{}, fmt.Errorf("invalid ai profile synthesis response")
	}
	return out, nil
}

func (h Handler) SynthesizeAIProfile(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.UserID(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var in SynthesizeAIProfileRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in); err != nil {
		http.Error(w, "invalid AI profile request", http.StatusBadRequest)
		return
	}
	out, err := synthesizeAIProfile(r.Context(), h.Provider, in)
	if err != nil {
		if strings.Contains(err.Error(), "at most") {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		} else {
			http.Error(w, "ai_profile_synthesis_failed", http.StatusBadGateway)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
