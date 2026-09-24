package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type Conversation struct{ ent.Schema }

func (Conversation) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }

func (Conversation) Fields() []ent.Field {
	return []ent.Field{
		field.String("title").Default(""),
		field.Text("summary").Default(""),
		field.Time("started_at"),
		field.Time("ended_at").Optional().Nillable(),
		field.Enum("status").Values("in_progress", "completed", "failed").Default("in_progress"),
	}
}

func (Conversation) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("conversations").Unique(), edge.From("device", Device.Type).Ref("conversations").Unique(), edge.To("transcript_segments", TranscriptSegment.Type), edge.To("memories", Memory.Type), edge.To("todos", Todo.Type), edge.To("action_items", ActionItem.Type)}
}
