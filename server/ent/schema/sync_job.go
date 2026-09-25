package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SyncJob struct{ ent.Schema }

func (SyncJob) Mixin() []ent.Mixin { return []ent.Mixin{TimestampMixin{}} }

func (SyncJob) Fields() []ent.Field {
	return []ent.Field{
		field.String("job_id").NotEmpty(),
		field.String("uid").NotEmpty(),
		field.String("conversation_id").Optional(),
		field.String("status").Default("queued"),
		field.Int("total_segments").Default(0),
		field.Int("processed_segments").Default(0),
		field.Int("successful_segments").Default(0),
		field.Int("failed_segments").Default(0),
		field.String("lane").Default("fresh"),
		field.String("reason_code").Optional(),
		field.Int("retry_after").Optional(),
		field.Int("recording_age_seconds").Optional(),
		field.String("error").Optional(),
		field.JSON("result", map[string]any{}).Optional(),
	}
}

func (SyncJob) Indexes() []ent.Index {
	return []ent.Index{index.Fields("job_id").Unique(), index.Fields("uid", "created_at")}
}
