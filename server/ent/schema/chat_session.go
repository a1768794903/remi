package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ChatSession struct{ ent.Schema }

func (ChatSession) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }

func (ChatSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("external_id").Unique(),
		field.String("title").Default("New Chat"),
		field.String("app_id").Optional().Nillable(),
		field.Bool("starred").Default(false),
		field.Int("user_id").Optional().Nillable(),
	}
}

func (ChatSession) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "updated_at")}
}

func (ChatSession) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("chat_sessions").Unique().Field("user_id"),
	}
}
