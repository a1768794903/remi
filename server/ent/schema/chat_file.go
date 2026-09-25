package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type ChatFile struct{ ent.Schema }
func (ChatFile) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }
func (ChatFile) Fields() []ent.Field { return []ent.Field{field.String("external_id").Unique(),field.String("name"),field.String("thumbnail").Default(""),field.String("mime_type"),field.String("openai_file_id"),field.String("thumb_name").Default(""),field.Int("user_id").Optional().Nillable()} }
func (ChatFile) Edges() []ent.Edge { return []ent.Edge{edge.From("user",User.Type).Ref("chat_files").Unique().Field("user_id")} }
