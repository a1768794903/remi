package chat

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"remi/server/ent"
	"remi/server/ent/chatmessage"
	"remi/server/ent/user"
)

var ErrNotFound = errors.New("chat message not found")

type Message struct {
	ID            string    `json:"id"`
	Text          string    `json:"text"`
	CreatedAt     time.Time `json:"created_at"`
	Sender        string    `json:"sender"`
	Type          string    `json:"type"`
	AppID         *string   `json:"app_id,omitempty"`
	ChatSessionID *string   `json:"chat_session_id,omitempty"`
	Rating        *int      `json:"rating,omitempty"`
	Reported      bool      `json:"reported"`
}
type SendInput struct {
	Text          string `json:"text"`
	AppID         string `json:"app_id"`
	PluginID      string `json:"plugin_id"`
	ChatSessionID string `json:"chat_session_id"`
}
type GenerateInput struct {
	Text    string `json:"text"`
	AppID   string `json:"app_id"`
	History []Turn `json:"history"`
}
type Service struct {
	Client   *ent.Client
	DB       *sql.DB
	Provider Provider
	Usage    UsageRecorder
}

type UsageRecorder interface {
	RecordChat(context.Context, string, int64) error
}
type DetailedUsageRecorder interface {
	RecordChatDetailed(context.Context, string, int64, int64, int64) error
}

func (s Service) complete(ctx context.Context, turns []Turn) (string, CompletionUsage, error) {
	if provider, ok := s.Provider.(UsageCompleter); ok {
		return provider.CompleteWithUsage(ctx, turns)
	}
	answer, err := s.Provider.Complete(ctx, turns)
	return answer, CompletionUsage{}, err
}

func (s Service) owner(ctx context.Context, uid string) (*ent.User, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		node, err = s.Client.User.Create().SetExternalUID(uid).SetEmail(uid).SetName(uid).Save(ctx)
	}
	return node, err
}
func (s Service) History(ctx context.Context, uid string, limit, offset int) ([]Message, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	nodes, err := s.Client.ChatMessage.Query().Where(chatmessage.HasUserWith(user.IDEQ(u.ID))).Order(ent.Asc(chatmessage.FieldCreatedAt)).Limit(limit).Offset(max(offset, 0)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, toMessage(n))
	}
	return out, nil
}

func (s Service) Shared(ctx context.Context, uid string, ids []string) ([]Message, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return nil, err
	}
	nodes, err := s.Client.ChatMessage.Query().Where(chatmessage.ExternalIDIn(ids...), chatmessage.HasUserWith(user.IDEQ(u.ID))).Order(ent.Asc(chatmessage.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, toMessage(n))
	}
	return out, nil
}
func (s Service) Send(ctx context.Context, uid string, input SendInput) (Message, error) {
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return Message{}, errors.New("text is required")
	}
	u, err := s.owner(ctx, uid)
	if err != nil {
		return Message{}, err
	}
	app := input.AppID
	if app == "" {
		app = input.PluginID
	}
	humanID := uuid.NewString()
	b := s.Client.ChatMessage.Create().SetExternalID(humanID).SetText(text).SetSender(chatmessage.SenderHuman).SetType(chatmessage.TypeText).SetUserID(u.ID)
	if app != "" {
		b.SetAppID(app)
	}
	if input.ChatSessionID != "" {
		b.SetChatSessionID(input.ChatSessionID)
	}
	human, err := b.Save(ctx)
	if err != nil {
		return Message{}, err
	}
	history, err := s.History(ctx, uid, 50, 0)
	if err != nil {
		return Message{}, err
	}
	turns := make([]Turn, 0, len(history)+1)
	for _, m := range history {
		turns = append(turns, Turn{Role: map[string]string{"human": "user", "ai": "assistant"}[m.Sender], Content: m.Text})
	}
	answer, completionUsage, err := s.complete(ctx, turns)
	if err != nil {
		return toMessage(human), err
	}
	ai := s.Client.ChatMessage.Create().SetExternalID(uuid.NewString()).SetText(answer).SetSender(chatmessage.SenderAi).SetType(chatmessage.TypeText).SetUserID(u.ID)
	if app != "" {
		ai.SetAppID(app)
	}
	if input.ChatSessionID != "" {
		ai.SetChatSessionID(input.ChatSessionID)
	}
	response, err := ai.Save(ctx)
	if err != nil {
		return Message{}, err
	}
	if s.Usage != nil {
		if detailed, ok := s.Usage.(DetailedUsageRecorder); ok {
			_ = detailed.RecordChatDetailed(ctx, uid, completionUsage.InputTokens, completionUsage.OutputTokens, completionUsage.CostMicroUSD)
		} else {
			_ = s.Usage.RecordChat(ctx, uid, completionUsage.CostMicroUSD)
		}
	}
	return toMessage(response), nil
}

func (s Service) RecordUsage(ctx context.Context, uid string) {
	if s.Usage != nil {
		_ = s.Usage.RecordChat(ctx, uid, 0)
	}
}
func (s Service) Generate(ctx context.Context, input GenerateInput) (string, error) {
	answer, _, err := s.GenerateWithUsage(ctx, input)
	return answer, err
}

func (s Service) GenerateWithUsage(ctx context.Context, input GenerateInput) (string, CompletionUsage, error) {
	text, err := trimText(input.Text)
	if err != nil {
		return "", CompletionUsage{}, err
	}
	turns := append([]Turn{}, input.History...)
	turns = append(turns, Turn{Role: "user", Content: text})
	return s.complete(ctx, turns)
}
func (s Service) Initial(ctx context.Context, uid, appID, sessionID string) (Message, error) {
	answer, err := s.Provider.Complete(ctx, []Turn{{Role: "user", Content: "Please greet me briefly."}})
	if err != nil {
		return Message{}, err
	}
	return s.Save(ctx, uid, SaveInput{Text: answer, Sender: "ai", AppID: appID, SessionID: sessionID})
}
func (s Service) DeleteAll(ctx context.Context, uid string) error {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return err
	}
	_, err = s.Client.ChatMessage.Delete().Where(chatmessage.HasUserWith(user.IDEQ(u.ID))).Exec(ctx)
	return err
}
func (s Service) Report(ctx context.Context, uid, id, reason string) error {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return err
	}
	n, err := s.Client.ChatMessage.Query().Where(chatmessage.ExternalIDEQ(id), chatmessage.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	if ent.IsNotFound(err) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = s.Client.ChatMessage.UpdateOneID(n.ID).SetReported(true).SetNillableReportReason(&reason).Save(ctx)
	return err
}
func toMessage(n *ent.ChatMessage) Message {
	return Message{ID: n.ExternalID, Text: n.Text, CreatedAt: n.CreatedAt, Sender: string(n.Sender), Type: string(n.Type), AppID: n.AppID, ChatSessionID: n.ChatSessionID, Rating: n.Rating, Reported: n.Reported}
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
