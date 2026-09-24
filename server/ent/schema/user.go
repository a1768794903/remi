package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type User struct{ ent.Schema }

func (User) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("email").NotEmpty(),
		field.String("name").Default(""),
	}
}

func (User) Indexes() []ent.Index {
	return []ent.Index{index.Fields("email").Unique()}
}
