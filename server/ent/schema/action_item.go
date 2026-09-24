package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// ActionItem mirrors the canonical task model used by the Python action_items
// router. Provenance remains JSON because evidence has several client variants.
type ActionItem struct{ ent.Schema }

func (ActionItem) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }

func (ActionItem) Fields() []ent.Field {
	return []ent.Field{
		field.String("description").NotEmpty(),
		field.Enum("status").Values("active", "completed", "cancelled", "superseded").Default("active"),
		field.Enum("owner").Values("user", "other", "unknown").Default("user"),
		field.Enum("priority").Values("high", "medium", "low").Optional().Nillable(),
		field.Float("due_confidence").Optional().Nillable(),
		field.String("source").Default("manual"),
		field.JSON("provenance", []map[string]any{}).Optional(),
		field.Int("sort_order").Default(0),
		field.Int("indent_level").Range(0, 3).Default(0),
		field.String("recurrence_rule").Optional().Nillable(),
		field.Int64("recurrence_parent_id").Optional().Nillable(),
		field.Time("due_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.Int64("superseded_by").Optional().Nillable(),
		field.Bool("is_locked").Default(false),
		field.Bool("exported").Default(false),
		field.Time("export_date").Optional().Nillable(),
		field.String("export_platform").Optional().Nillable(),
		field.String("apple_reminder_id").Optional().Nillable(),
	}
}

func (ActionItem) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("action_items").Unique(),
		edge.From("conversation", Conversation.Type).Ref("action_items").Unique(),
	}
}
