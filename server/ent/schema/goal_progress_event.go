package schema

import("entgo.io/ent";"entgo.io/ent/schema/edge";"entgo.io/ent/schema/field";"entgo.io/ent/schema/index")
type GoalProgressEvent struct{ent.Schema}
func(GoalProgressEvent)Mixin()[]ent.Mixin{return []ent.Mixin{TimestampMixin{}}}
func(GoalProgressEvent)Fields()[]ent.Field{return []ent.Field{field.String("external_id").Unique(),field.Int("sequence"),field.Enum("kind").Values("evidence","metric_update","milestone","status_change"),field.String("summary").NotEmpty(),field.JSON("evidence_refs",[]map[string]any{}).Optional(),field.JSON("metric",map[string]any{}).Optional(),field.Int("goal_id")}}
func(GoalProgressEvent)Indexes()[]ent.Index{return []ent.Index{index.Fields("goal_id","sequence").Unique()}}
func(GoalProgressEvent)Edges()[]ent.Edge{return []ent.Edge{edge.From("goal",Goal.Type).Ref("progress_events").Unique().Field("goal_id").Required()}}
