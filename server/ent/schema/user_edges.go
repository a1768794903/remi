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
	}
}
