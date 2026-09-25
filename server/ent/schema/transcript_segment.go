package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type TranscriptSegment struct{ ent.Schema }

func (TranscriptSegment) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }

func (TranscriptSegment) Fields() []ent.Field {
	return []ent.Field{
		field.String("speaker").Default("unknown"),
		field.Int("speaker_id").Default(0),
		field.Bool("is_user").Default(false),
		field.String("person_id").Optional().Nillable(),
		field.Text("text"),
		field.Int64("start_ms").NonNegative(),
		field.Int64("end_ms").NonNegative(),
		field.String("source").Default("stt"),
		field.Int("conversation_id").Optional().Nillable(),
	}
}

func (TranscriptSegment) Edges() []ent.Edge {
	return []ent.Edge{edge.From("conversation", Conversation.Type).Ref("transcript_segments").Unique().Field("conversation_id")}
}
