package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Folder struct{ ent.Schema }
func (Folder) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }
func (Folder) Fields() []ent.Field { return []ent.Field{
	field.String("external_id").Unique(), field.String("name").NotEmpty(), field.String("description").Optional().Nillable(), field.String("color").Default("#6B7280"), field.String("icon").Default("folder"), field.Int("order").Default(0), field.Bool("is_default").Default(false), field.Bool("is_system").Default(false), field.String("category_mapping").Optional().Nillable(), field.Int("user_id").Optional().Nillable(),
} }
func (Folder) Indexes() []ent.Index { return []ent.Index{index.Fields("user_id", "order")} }
func (Folder) Edges() []ent.Edge { return []ent.Edge{edge.From("user", User.Type).Ref("folders").Unique().Field("user_id"), edge.To("conversations", Conversation.Type)} }
