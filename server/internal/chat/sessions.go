package chat

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"remi/server/ent"
	"remi/server/ent/chatmessage"
	"remi/server/ent/chatsession"
	"remi/server/ent/user"
)

var ErrSessionNotFound = errors.New("chat session not found")

type Session struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	AppID   *string `json:"app_id,omitempty"`
	Starred bool    `json:"starred"`
}

type SessionService struct {
	Client   *ent.Client
	Provider Provider
}

func (s SessionService) owner(ctx context.Context, uid string) (*ent.User, error) {
	node, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return s.Client.User.Create().SetExternalUID(uid).SetEmail(uid).SetName(uid).Save(ctx)
	}
	return node, err
}

func (s SessionService) Create(ctx context.Context, uid, title, appID string) (Session, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return Session{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "New Chat"
	}
	b := s.Client.ChatSession.Create().SetExternalID(uuid.NewString()).SetTitle(title).SetUserID(u.ID)
	if appID != "" {
		b.SetAppID(appID)
	}
	n, err := b.Save(ctx)
	if err != nil {
		return Session{}, err
	}
	return toSession(n), nil
}

func (s SessionService) List(ctx context.Context, uid, appID string, limit, offset int, starred *bool) ([]Session, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	q := s.Client.ChatSession.Query().Where(chatsession.HasUserWith(user.IDEQ(u.ID))).Order(ent.Desc(chatsession.FieldUpdatedAt)).Limit(limit).Offset(offset)
	if appID != "" {
		q = q.Where(chatsession.AppIDEQ(appID))
	}
	if starred != nil {
		q = q.Where(chatsession.StarredEQ(*starred))
	}
	nodes, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, toSession(n))
	}
	return out, nil
}

func (s SessionService) Get(ctx context.Context, uid, id string) (Session, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return Session{}, err
	}
	n, err := s.Client.ChatSession.Query().Where(chatsession.ExternalIDEQ(id), chatsession.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	if ent.IsNotFound(err) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, err
	}
	return toSession(n), nil
}

func (s SessionService) Update(ctx context.Context, uid, id string, title *string, starred *bool) (Session, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return Session{}, err
	}
	n, err := s.Client.ChatSession.Query().Where(chatsession.ExternalIDEQ(id), chatsession.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	if ent.IsNotFound(err) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, err
	}
	b := s.Client.ChatSession.UpdateOneID(n.ID)
	if title != nil {
		b.SetTitle(strings.TrimSpace(*title))
	}
	if starred != nil {
		b.SetStarred(*starred)
	}
	n, err = b.Save(ctx)
	if err != nil {
		return Session{}, err
	}
	return toSession(n), nil
}

func (s SessionService) Delete(ctx context.Context, uid, id string) error {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return err
	}
	n, err := s.Client.ChatSession.Query().Where(chatsession.ExternalIDEQ(id), chatsession.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = s.Client.ChatMessage.Delete().Where(chatmessage.ChatSessionIDEQ(id), chatmessage.HasUserWith(user.IDEQ(u.ID))).Exec(ctx); err != nil {
		return err
	}
	return s.Client.ChatSession.DeleteOneID(n.ID).Exec(ctx)
}

func (s SessionService) Count(ctx context.Context, uid string) (int, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return 0, err
	}
	return s.Client.ChatMessage.Query().Where(chatmessage.HasUserWith(user.IDEQ(u.ID))).Count(ctx)
}

func (s SessionService) Initial(ctx context.Context, uid, appID, sessionID string) (Message, error) {
	answer, err := s.Provider.Complete(ctx, []Turn{{Role: "user", Content: "Please greet me briefly."}})
	if err != nil {
		return Message{}, err
	}
	return (Service{Client: s.Client, Provider: s.Provider}).Save(ctx, uid, SaveInput{Text: answer, Sender: "ai", AppID: appID, SessionID: sessionID})
}

func toSession(n *ent.ChatSession) Session {
	return Session{ID: n.ExternalID, Title: n.Title, AppID: n.AppID, Starred: n.Starred}
}
