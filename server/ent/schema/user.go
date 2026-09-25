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
		// Firebase UID is the stable identity used by the Python service and
		// must not be derived from the local auto-increment primary key.
		field.String("external_uid").NotEmpty(),
		field.String("email").NotEmpty(),
		field.String("name").Default(""),
		field.String("language").Default("en"),
		field.String("time_zone").Default("UTC"),
		field.JSON("onboarding", map[string]any{}).Optional(),
		field.Bool("private_cloud_sync_enabled").Default(true),
		field.Bool("meeting_note_screenshots_enabled").Default(true),
		field.Bool("store_recording_permission").Default(false),
		field.Bool("daily_summary_enabled").Default(true),
		field.Int("daily_summary_hour_local").Default(22),
		field.Int("mentor_notification_frequency").Default(0),
		field.JSON("integrations", map[string]any{}).Optional(),
		field.JSON("notification_settings", map[string]any{"enabled": true, "frequency": 0}).Optional(),
		field.JSON("assistant_settings", map[string]any{}).Optional(),
		field.JSON("ai_profile", map[string]any{}).Optional(),
	}
}


func (User) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("external_uid").Unique(),
		index.Fields("email").Unique(),
	}
}
