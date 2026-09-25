package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type Memory struct{ ent.Schema }

func (Memory) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }

func (Memory) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("type").Values("fact", "decision", "preference", "event").Default("fact"),
		field.String("category").Default("interesting"),
		field.String("visibility").Default("private"),
		field.JSON("tags", []string{}).Optional(),
		field.Bool("is_read").Default(false),
		field.Bool("is_dismissed").Default(false),
		field.Text("content"),
		field.Int("importance").Range(0, 100).Default(50),
		field.Time("event_time").Optional().Nillable(),
		field.Int("user_id").Optional().Nillable(),
		field.Int("conversation_id").Optional().Nillable(),
	}
}

func (Memory) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("memories").Unique().Field("user_id"),
		edge.From("conversation", Conversation.Type).Ref("memories").Unique().Field("conversation_id"),
	}
}
