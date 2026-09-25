package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
)

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("devices", Device.Type),
		edge.To("conversations", Conversation.Type),
		edge.To("memories", Memory.Type),
		edge.To("todos", Todo.Type),
		edge.To("action_items", ActionItem.Type),
		edge.To("chat_messages", ChatMessage.Type),
		edge.To("chat_sessions", ChatSession.Type),
		edge.To("notification_tokens", NotificationToken.Type),
		edge.To("folders", Folder.Type),
		edge.To("goals", Goal.Type),
		edge.To("calendar_meetings", CalendarMeeting.Type),
		edge.To("csat_ratings", CsatRating.Type),
		edge.To("chat_files", ChatFile.Type),
	}
}
