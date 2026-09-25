package finalization

import (
	"context"
	"errors"
	"strings"

	"remi/server/internal/actionitems"
	"remi/server/internal/chat"
	"remi/server/internal/conversations"
	"remi/server/internal/memories"
	"remi/server/internal/transcripts"
)

var ErrProviderUnavailable = errors.New("finalization provider is not configured")

type Processor struct {
	Conversations conversations.Service
	Transcripts   transcripts.Service
	Memories      memories.Service
	ActionItems   actionitems.Service
	Provider      chat.Provider
}

func (p Processor) Process(ctx context.Context, uid, conversationID string) error {
	segments, err := p.Transcripts.List(ctx, uid, conversationID)
	if err != nil {
		return err
	}
	if p.Provider == nil {
		return ErrProviderUnavailable
	}
	lines := make([]TranscriptLine, 0, len(segments))
	for _, segment := range segments {
		lines = append(lines, TranscriptLine{Speaker: segment.Speaker, Text: segment.Text})
	}
	turns := []chat.Turn{
		{Role: "system", Content: "You are Remi's conversation finalizer. Return only strict JSON with fields summary (string), memories (array of objects with type/content/category/visibility/importance), and action_items (array of objects with description/owner/status). Do not invent facts not grounded in the transcript."},
		{Role: "user", Content: BuildTranscriptPrompt(lines)},
	}
	raw, err := p.Provider.Complete(ctx, turns)
	if err != nil {
		return err
	}
	result, err := ParseResult(raw)
	if err != nil {
		return err
	}
	if err := p.persistDerived(ctx, uid, conversationID, result); err != nil {
		return err
	}
	summary := strings.TrimSpace(result.Summary)
	_, err = p.Conversations.Update(ctx, uid, conversationID, conversations.UpdateInput{Summary: &summary})
	return err
}

func (p Processor) persistDerived(ctx context.Context, uid, conversationID string, result Result) error {
	for _, draft := range result.Memories {
		memoryType := draft.Type
		if memoryType == "" {
			memoryType = "fact"
		}
		if _, err := p.Memories.Create(ctx, uid, memories.CreateInput{Type: memoryType, Content: draft.Content, Category: draft.Category, Visibility: draft.Visibility, Importance: draft.Importance, ConversationID: &conversationID}); err != nil {
			return err
		}
	}
	for _, draft := range result.ActionItems {
		owner := draft.Owner
		if owner == "" {
			owner = "user"
		}
		status := draft.Status
		if status == "" {
			status = "active"
		}
		if _, err := p.ActionItems.Create(ctx, uid, actionitems.CreateInput{Description: draft.Description, Owner: owner, Status: status, Source: "conversation_finalization", ConversationID: &conversationID}); err != nil {
			return err
		}
	}
	return nil
}
