package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type CalendarMeeting struct{ ent.Schema }

func (CalendarMeeting) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }
func (CalendarMeeting) Fields() []ent.Field {
	return []ent.Field{
		field.String("external_id").Unique(),
		field.String("calendar_event_id").NotEmpty(),
		field.String("calendar_source").Default("system_calendar"),
		field.String("title").NotEmpty(),
		field.JSON("participants", []map[string]any{}).Optional(),
		field.String("platform").Optional().Nillable(),
		field.String("meeting_link").Optional().Nillable(),
		field.Time("start_time"),
		field.Time("end_time"),
		field.Int("duration_minutes"),
		field.Text("notes").Optional().Nillable(),
		field.Time("synced_at"),
		field.Int("user_id").Optional().Nillable(),
	}
}
func (CalendarMeeting) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("calendar_meetings").Unique().Field("user_id")}
}
