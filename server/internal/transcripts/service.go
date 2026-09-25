package transcripts

import (
	"context"
	"errors"
	"strconv"
	"time"

	"remi/server/ent"
	"remi/server/ent/conversation"
	"remi/server/ent/transcriptsegment"
	"remi/server/ent/user"
)

var ErrNotFound = errors.New("transcript segment not found")

type Segment struct {
	ID        string    `json:"id"`
	Speaker   string    `json:"speaker"`
	SpeakerID int       `json:"speaker_id"`
	IsUser    bool      `json:"is_user"`
	PersonID  *string   `json:"person_id,omitempty"`
	Text      string    `json:"text"`
	StartMs   int64     `json:"start_ms"`
	EndMs     int64     `json:"end_ms"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateInput struct {
	Speaker   string  `json:"speaker"`
	SpeakerID int     `json:"speaker_id"`
	IsUser    bool    `json:"is_user"`
	PersonID  *string `json:"person_id"`
	Text      string  `json:"text"`
	StartMs   int64   `json:"start_ms"`
	EndMs     int64   `json:"end_ms"`
	Source    string  `json:"source"`
}

type Service struct{ Client *ent.Client }

func (s Service) AssignSegment(ctx context.Context, uid, conversationID string, index int, assignType, value string) error {
	segments, err := s.List(ctx, uid, conversationID)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(segments) {
		return ErrNotFound
	}
	id, _ := strconv.Atoi(segments[index].ID)
	update := s.Client.TranscriptSegment.UpdateOneID(id)
	if assignType == "is_user" {
		update.SetIsUser(value == "true" || value == "1").ClearPersonID()
	} else if assignType == "person_id" && value != "" && value != "null" {
		update.SetIsUser(false).SetPersonID(value)
	} else {
		return errors.New("invalid assign_type")
	}
	_, err = update.Save(ctx)
	return err
}

func (s Service) AssignSpeaker(ctx context.Context, uid, conversationID string, speakerID int, assignType, value string) error {
	conversationNode, err := s.conversation(ctx, uid, conversationID)
	if err != nil {
		return err
	}
	nodes, err := s.Client.TranscriptSegment.Query().Where(transcriptsegment.HasConversationWith(conversation.IDEQ(conversationNode.ID)), transcriptsegment.SpeakerIDEQ(speakerID)).All(ctx)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if err := s.applyAssignment(ctx, node.ID, assignType, value); err != nil {
			return err
		}
	}
	return nil
}

func (s Service) AssignBulk(ctx context.Context, uid, conversationID string, ids []string, assignType, value string) error {
	conversationNode, err := s.conversation(ctx, uid, conversationID)
	if err != nil {
		return err
	}
	for _, rawID := range ids {
		id, parseErr := strconv.Atoi(rawID)
		if parseErr != nil {
			return ErrNotFound
		}
		node, getErr := s.Client.TranscriptSegment.Query().Where(transcriptsegment.IDEQ(id), transcriptsegment.HasConversationWith(conversation.IDEQ(conversationNode.ID))).Only(ctx)
		if ent.IsNotFound(getErr) {
			return ErrNotFound
		}
		if getErr != nil {
			return getErr
		}
		if err := s.applyAssignment(ctx, node.ID, assignType, value); err != nil {
			return err
		}
	}
	return nil
}

func (s Service) applyAssignment(ctx context.Context, id int, assignType, value string) error {
	update := s.Client.TranscriptSegment.UpdateOneID(id)
	switch assignType {
	case "is_user":
		update.SetIsUser(value == "true" || value == "1").ClearPersonID()
	case "person_id":
		if value == "" || value == "null" {
			update.SetIsUser(false).ClearPersonID()
		} else {
			update.SetIsUser(false).SetPersonID(value)
		}
	default:
		return errors.New("invalid assign_type")
	}
	_, err := update.Save(ctx)
	return err
}

func (s Service) conversation(ctx context.Context, uid, id string) (*ent.Conversation, error) {
	u, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if err != nil {
		return nil, ErrNotFound
	}
	numericID, err := strconv.Atoi(id)
	if err != nil {
		return nil, ErrNotFound
	}
	node, err := s.Client.Conversation.Query().Where(conversation.IDEQ(numericID), conversation.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return node, err
}

func (s Service) Create(ctx context.Context, uid, conversationID string, input CreateInput) (Segment, error) {
	conversationNode, err := s.conversation(ctx, uid, conversationID)
	if err != nil {
		return Segment{}, err
	}
	source := input.Source
	if source == "" {
		source = "stt"
	}
	speaker := input.Speaker
	if speaker == "" {
		speaker = "SPEAKER_00"
	}
	builder := s.Client.TranscriptSegment.Create().SetConversationID(conversationNode.ID).SetSpeaker(speaker).SetSpeakerID(input.SpeakerID).SetIsUser(input.IsUser).SetText(input.Text).SetStartMs(input.StartMs).SetEndMs(input.EndMs).SetSource(source)
	if input.PersonID != nil {
		builder.SetPersonID(*input.PersonID)
	}
	node, err := builder.Save(ctx)
	if err != nil {
		return Segment{}, err
	}
	return toSegment(node), nil
}

func (s Service) List(ctx context.Context, uid, conversationID string) ([]Segment, error) {
	conversationNode, err := s.conversation(ctx, uid, conversationID)
	if err != nil {
		return nil, err
	}
	nodes, err := s.Client.TranscriptSegment.Query().Where(transcriptsegment.HasConversationWith(conversation.IDEQ(conversationNode.ID))).Order(ent.Asc(transcriptsegment.FieldStartMs)).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Segment, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, toSegment(node))
	}
	return result, nil
}

func (s Service) UpdateText(ctx context.Context, uid, conversationID, segmentID, text string) error {
	if _, err := s.conversation(ctx, uid, conversationID); err != nil {
		return err
	}
	id, err := strconv.Atoi(segmentID)
	if err != nil {
		return ErrNotFound
	}
	updated, err := s.Client.TranscriptSegment.UpdateOneID(id).Where(transcriptsegment.HasConversationWith(conversation.IDEQ(parseConversationID(conversationID)))).SetText(text).Save(ctx)
	if ent.IsNotFound(err) || updated == nil {
		return ErrNotFound
	}
	return err
}

func parseConversationID(id string) int {
	value, _ := strconv.Atoi(id)
	return value
}

func toSegment(node *ent.TranscriptSegment) Segment {
	return Segment{ID: strconv.Itoa(node.ID), Speaker: node.Speaker, SpeakerID: node.SpeakerID, IsUser: node.IsUser, PersonID: node.PersonID, Text: node.Text, StartMs: node.StartMs, EndMs: node.EndMs, Source: node.Source, CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt}
}
