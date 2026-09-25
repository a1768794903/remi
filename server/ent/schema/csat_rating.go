package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type CsatRating struct{ ent.Schema }
func (CsatRating) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }
func (CsatRating) Fields() []ent.Field { return []ent.Field{field.String("external_id").Unique(), field.String("platform"), field.String("app_version").Default(""), field.Int("score"), field.Text("comment").Default(""), field.Int("revision").Default(0), field.Int("user_id").Optional().Nillable()} }
func (CsatRating) Edges() []ent.Edge { return []ent.Edge{edge.From("user", User.Type).Ref("csat_ratings").Unique().Field("user_id")} }
func (CsatRating) Indexes() []ent.Index { return []ent.Index{index.Fields("user_id", "platform").Unique()} }
