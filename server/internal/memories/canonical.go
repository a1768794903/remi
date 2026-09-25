package memories

import (
	"errors"
	"fmt"
	"strings"
)

const MaxFeedbackIDLength = 128

type MemoryUseAction string

const (
	MemoryUseSuppress MemoryUseAction = "suppress"
	MemoryUseAllow    MemoryUseAction = "allow"
	MemoryUseUseful   MemoryUseAction = "useful"
)

var ErrFeedbackConflict = errors.New("feedback_id was already used for a different action")

func NormalizeMemoryUseAction(value string) (MemoryUseAction, error) {
	switch MemoryUseAction(strings.ToLower(strings.TrimSpace(value))) {
	case MemoryUseSuppress:
		return MemoryUseSuppress, nil
	case MemoryUseAllow:
		return MemoryUseAllow, nil
	case MemoryUseUseful:
		return MemoryUseUseful, nil
	default:
		return "", errors.New("action must be suppress, allow, or useful")
	}
}

func NormalizeFeedbackID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("feedback_id must not be blank")
	}
	if len([]rune(value)) > MaxFeedbackIDLength {
		return "", fmt.Errorf("feedback_id must be at most %d characters", MaxFeedbackIDLength)
	}
	return value, nil
}

type MemoryUseState struct {
	State      string `json:"state"`
	Suppressed bool   `json:"suppressed"`
	LastAction string `json:"last_action"`
	FeedbackID string `json:"feedback_id"`
}

func BuildMemoryUseState(existing MemoryUseState, action MemoryUseAction, feedbackID string) (MemoryUseState, int, error) {
	feedbackID, err := NormalizeFeedbackID(feedbackID)
	if err != nil {
		return MemoryUseState{}, 0, err
	}
	if existing.FeedbackID == feedbackID && existing.LastAction != "" && existing.LastAction != string(action) {
		return MemoryUseState{}, 0, ErrFeedbackConflict
	}
	suppressed := action == MemoryUseSuppress
	if action == MemoryUseUseful {
		suppressed = existing.Suppressed
	}
	state := "allowed"
	if suppressed {
		state = "suppressed"
	} else if action == MemoryUseUseful {
		state = "useful"
	}
	weight := 0
	if action == MemoryUseUseful {
		weight = 1
	}
	return MemoryUseState{State: state, Suppressed: suppressed, LastAction: string(action), FeedbackID: feedbackID}, weight, nil
}
