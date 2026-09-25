package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Goal struct{ ent.Schema }
func (Goal) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }
func (Goal) Fields() []ent.Field { return []ent.Field{
	field.String("external_id").Unique(), field.String("title").NotEmpty(), field.String("desired_outcome").Default(""), field.String("why_it_matters").Optional().Nillable(), field.JSON("success_criteria", []string{}).Optional(), field.Time("horizon_at").Optional().Nillable(), field.Enum("status").Values("background","focused","paused","achieved","abandoned").Default("background"), field.Int("focus_rank").Optional().Nillable(), field.JSON("metric", map[string]any{}).Optional(), field.Enum("source").Values("user","ai_suggested","imported").Default("user"), field.Time("ended_at").Optional().Nillable(), field.Int("user_id").Optional().Nillable(),
} }
func (Goal) Indexes() []ent.Index { return []ent.Index{index.Fields("user_id", "status")} }
func (Goal) Edges() []ent.Edge { return []ent.Edge{edge.From("user", User.Type).Ref("goals").Unique().Field("user_id"), edge.To("progress_events", GoalProgressEvent.Type)} }
