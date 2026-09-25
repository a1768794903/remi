package chat

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"remi/server/ent"
	"remi/server/ent/chatmessage"
	"remi/server/ent/user"
)

type SaveInput struct{ Text, Sender, AppID, SessionID, ClientMessageID string }

func (s Service) Save(ctx context.Context, uid string, in SaveInput) (Message, error) {
	text, err := trimText(in.Text)
	if err != nil {
		return Message{}, err
	}
	if !validateSender(in.Sender) {
		return Message{}, errors.New("sender must be human or ai")
	}
	u, err := s.owner(ctx, uid)
	if err != nil {
		return Message{}, err
	}
	id := in.ClientMessageID
	if id == "" {
		id = uuid.NewString()
	}
	q, qerr := s.Client.ChatMessage.Query().Where(chatmessage.ExternalIDEQ(id), chatmessage.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	if qerr == nil {
		return toMessage(q), nil
	}
	if !ent.IsNotFound(qerr) {
		return Message{}, qerr
	}
	b := s.Client.ChatMessage.Create().SetExternalID(id).SetText(text).SetSender(chatmessage.Sender(in.Sender)).SetType(chatmessage.TypeText).SetUserID(u.ID)
	if in.AppID != "" {
		b.SetAppID(in.AppID)
	}
	if in.SessionID != "" {
		b.SetChatSessionID(in.SessionID)
	}
	n, err := b.Save(ctx)
	if err != nil {
		return Message{}, err
	}
	return toMessage(n), nil
}

func (s Service) Reconcile(ctx context.Context, uid, appID, sessionID, cursor string, limit int) ([]Message, *string, bool, error) {
	items, err := s.DesktopHistory(ctx, uid, appID, sessionID, 1001, 0)
	if err != nil {
		return nil, nil, false, err
	}
	start := 0
	if cursor != "" {
		for i, item := range items {
			if item.ID == cursor {
				start = i + 1
				break
			}
		}
		if start == 0 {
			return nil, nil, false, errors.New("invalid message reconciliation cursor")
		}
	}
	if limit < 1 || limit > 100 {
		limit = 100
	}
	items = items[start:]
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var next *string
	if hasMore && len(items) > 0 {
		next = &items[len(items)-1].ID
	}
	return items, next, hasMore, nil
}
func (s Service) DesktopHistory(ctx context.Context, uid, appID, sessionID string, limit, offset int) ([]Message, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	q := s.Client.ChatMessage.Query().Where(chatmessage.HasUserWith(user.IDEQ(u.ID))).Order(ent.Asc(chatmessage.FieldCreatedAt)).Limit(limit).Offset(max(offset, 0))
	if appID != "" {
		q = q.Where(chatmessage.AppIDEQ(appID))
	}
	if sessionID != "" {
		q = q.Where(chatmessage.ChatSessionIDEQ(sessionID))
	}
	nodes, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, toMessage(n))
	}
	return out, nil
}
func (s Service) DeleteDesktop(ctx context.Context, uid, appID, sessionID string) (int, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return 0, err
	}
	q := s.Client.ChatMessage.Delete().Where(chatmessage.HasUserWith(user.IDEQ(u.ID)))
	if appID != "" {
		q = q.Where(chatmessage.AppIDEQ(appID))
	}
	if sessionID != "" {
		q = q.Where(chatmessage.ChatSessionIDEQ(sessionID))
	}
	return q.Exec(ctx)
}
func (s Service) Rate(ctx context.Context, uid, id string, rating *int) error {
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
	b := s.Client.ChatMessage.UpdateOneID(n.ID)
	if rating == nil {
		b.ClearRating()
	} else {
		b.SetRating(*rating)
	}
	_, err = b.Save(ctx)
	if err == nil && s.DB != nil {
		value := any(nil)
		if rating != nil {
			value = *rating
		}
		_, err = s.DB.ExecContext(ctx, `INSERT INTO feedback_events(event_id,user_external_uid,surface,target_kind,target_id,value,created_at) VALUES(UUID(),?,?,?,?,?,UTC_TIMESTAMP(6))`, uid, "chat", "chat_message", id, value)
	}
	return err
}
