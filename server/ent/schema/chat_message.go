package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type ChatMessage struct{ ent.Schema }

func (ChatMessage) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }
func (ChatMessage) Fields() []ent.Field {
	return []ent.Field{
		field.String("external_id").Unique(),
		field.Text("text"),
		field.Enum("sender").Values("human", "ai"),
		field.Enum("type").Values("text", "day_summary").Default("text"),
		field.String("app_id").Optional().Nillable(),
		field.String("chat_session_id").Optional().Nillable(),
		field.Int("rating").Optional().Nillable(),
		field.Bool("reported").Default(false),
		field.String("report_reason").Optional().Nillable(),
		field.Int("user_id").Optional().Nillable(),
	}
}
func (ChatMessage) Edges() []ent.Edge { return []ent.Edge{edge.From("user", User.Type).Ref("chat_messages").Unique().Field("user_id")} }
