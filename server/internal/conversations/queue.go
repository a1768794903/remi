package conversations

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const FinalizationQueueKey = "remi:conversation-finalization"
const MergeQueueKey = "remi:conversation-merge"

type FinalizationJob struct {
	UID            string `json:"uid"`
	ConversationID string `json:"conversation_id"`
}
type MergeJob struct {
	UID       string   `json:"uid"`
	MergedID  string   `json:"merged_conversation_id"`
	SourceIDs []string `json:"source_conversation_ids"`
	Reprocess bool     `json:"reprocess"`
}
type Enqueuer interface {
	EnqueueFinalization(context.Context, FinalizationJob) error
}
type RedisQueue struct {
	Client *redis.Client
	Key    string
}

func (q RedisQueue) EnqueueFinalization(ctx context.Context, job FinalizationJob) error {
	if q.Client == nil {
		return errors.New("redis queue is not configured")
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return q.Client.RPush(ctx, q.key(), payload).Err()
}
func (q RedisQueue) EnqueueMerge(ctx context.Context, job MergeJob) error {
	if q.Client == nil {
		return errors.New("redis queue is not configured")
	}
	if job.UID == "" || job.MergedID == "" || len(job.SourceIDs) < 2 {
		return errors.New("invalid merge job")
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return q.Client.RPush(ctx, MergeQueueKey, payload).Err()
}
func (q RedisQueue) Receive(ctx context.Context, timeout time.Duration) (FinalizationJob, error) {
	if q.Client == nil {
		return FinalizationJob{}, errors.New("redis queue is not configured")
	}
	result, err := q.Client.BLPop(ctx, timeout, q.key()).Result()
	if err != nil {
		return FinalizationJob{}, err
	}
	if len(result) != 2 {
		return FinalizationJob{}, errors.New("invalid queue item")
	}
	var job FinalizationJob
	if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
		return FinalizationJob{}, err
	}
	if job.UID == "" || job.ConversationID == "" {
		return FinalizationJob{}, errors.New("invalid finalization job")
	}
	return job, nil
}
func (q RedisQueue) key() string {
	if q.Key != "" {
		return q.Key
	}
	return FinalizationQueueKey
}
