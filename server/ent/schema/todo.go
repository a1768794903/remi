package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type Todo struct{ ent.Schema }

func (Todo) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }

func (Todo) Fields() []ent.Field {
	return []ent.Field{
		field.String("title").NotEmpty(),
		field.Text("description").Default(""),
		field.Time("due_at").Optional().Nillable(),
		field.Enum("status").Values("open", "completed", "cancelled").Default("open"),
		field.Int("user_id").Optional().Nillable(),
		field.Int("conversation_id").Optional().Nillable(),
	}
}

func (Todo) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("todos").Unique().Field("user_id"), edge.From("conversation", Conversation.Type).Ref("todos").Unique().Field("conversation_id")}
}
