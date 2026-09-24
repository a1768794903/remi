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
	}
}

func (Todo) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("todos").Unique(), edge.From("conversation", Conversation.Type).Ref("todos").Unique()}
}
