package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Device struct{ ent.Schema }

func (Device) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }

func (Device) Fields() []ent.Field {
	return []ent.Field{
		field.String("device_id").NotEmpty(),
		field.String("name").Default("Remi Wearable"),
		field.String("firmware_version").Default(""),
		field.Int("battery_level").Default(-1),
		field.Time("last_seen_at").Optional().Nillable(),
		field.Int("user_id").Optional().Nillable(),
	}
}

func (Device) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("devices").Unique().Field("user_id"), edge.To("conversations", Conversation.Type)}
}

func (Device) Indexes() []ent.Index {
	return []ent.Index{index.Fields("device_id").Unique()}
}
