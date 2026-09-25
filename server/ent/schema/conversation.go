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
		field.String("visibility").Default("private"),
		field.Bool("starred").Default(false),
		field.Time("started_at"),
		field.Time("ended_at").Optional().Nillable(),
		field.Enum("status").Values("in_progress", "processing", "merging", "completed", "failed").Default("in_progress"),
		field.Int("user_id").Optional().Nillable(),
		field.Int("device_id").Optional().Nillable(),
		field.Int("folder_id").Optional().Nillable(),
		field.JSON("audio_files", []map[string]any{}).Optional(),
		field.JSON("conversation_audio", map[string]any{}).Optional(),
		field.Bool("screenshot_sharing_enabled").Default(false),
	}
}

func (Conversation) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("conversations").Unique().Field("user_id"),
		edge.From("device", Device.Type).Ref("conversations").Unique().Field("device_id"),
		edge.From("folder", Folder.Type).Ref("conversations").Unique().Field("folder_id"),
		edge.To("transcript_segments", TranscriptSegment.Type),
		edge.To("memories", Memory.Type),
		edge.To("todos", Todo.Type),
		edge.To("action_items", ActionItem.Type),
	}
}
