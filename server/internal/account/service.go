package account

import (
	"context"
	"errors"
	"remi/server/ent"
	"remi/server/ent/actionitem"
	"remi/server/ent/chatmessage"
	"remi/server/ent/chatsession"
	"remi/server/ent/conversation"
	"remi/server/ent/device"
	"remi/server/ent/folder"
	"remi/server/ent/goal"
	"remi/server/ent/memory"
	"remi/server/ent/notificationtoken"
	"remi/server/ent/todo"
	"remi/server/ent/transcriptsegment"
	"remi/server/ent/user"
)

var ErrNotFound = errors.New("user not found")

type Service struct{ Client *ent.Client }

func (s Service) Delete(ctx context.Context, uid string) error {
	u, e := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(e) {
		return ErrNotFound
	}
	if e != nil {
		return e
	}
	pred := user.IDEQ(u.ID)
	if _, e = s.Client.TranscriptSegment.Delete().Where(transcriptsegment.HasConversationWith(conversation.HasUserWith(pred))).Exec(ctx); e != nil {
		return e
	}
	if _, e = s.Client.Memory.Delete().Where(memory.HasUserWith(pred)).Exec(ctx); e != nil {
		return e
	}
	if _, e = s.Client.Todo.Delete().Where(todo.HasUserWith(pred)).Exec(ctx); e != nil {
		return e
	}
	if _, e = s.Client.ActionItem.Delete().Where(actionitem.HasUserWith(pred)).Exec(ctx); e != nil {
		return e
	}
	if _, e = s.Client.ChatMessage.Delete().Where(chatmessage.HasUserWith(pred)).Exec(ctx); e != nil {
		return e
	}
	if _, e = s.Client.ChatSession.Delete().Where(chatsession.HasUserWith(pred)).Exec(ctx); e != nil {
		return e
	}
	if _, e = s.Client.Goal.Delete().Where(goal.HasUserWith(pred)).Exec(ctx); e != nil {
		return e
	}
	if _, e = s.Client.Folder.Delete().Where(folder.HasUserWith(pred)).Exec(ctx); e != nil {
		return e
	}
	if _, e = s.Client.Conversation.Delete().Where(conversation.HasUserWith(pred)).Exec(ctx); e != nil {
		return e
	}
	if _, e = s.Client.Device.Delete().Where(device.HasUserWith(pred)).Exec(ctx); e != nil {
		return e
	}
	if _, e = s.Client.NotificationToken.Delete().Where(notificationtoken.HasUserWith(pred)).Exec(ctx); e != nil {
		return e
	}
	return s.Client.User.DeleteOneID(u.ID).Exec(ctx)
}
