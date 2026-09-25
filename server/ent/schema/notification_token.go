package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type NotificationToken struct{ ent.Schema }
func (NotificationToken) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }
func (NotificationToken) Fields() []ent.Field { return []ent.Field{
	field.String("token").NotEmpty(), field.String("platform").Default("unknown"), field.String("device_key").Default("default"), field.Int("user_id").Optional().Nillable(),
} }
func (NotificationToken) Indexes() []ent.Index { return []ent.Index{index.Fields("user_id", "device_key").Unique()} }
func (NotificationToken) Edges() []ent.Edge { return []ent.Edge{edge.From("user", User.Type).Ref("notification_tokens").Unique().Field("user_id")} }
