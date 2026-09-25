package conversations

import (
	"context"
	"testing"
	"time"
)

func TestRedisQueueRequiresClient(t *testing.T) {
	queue := RedisQueue{}
	if err := queue.EnqueueFinalization(context.Background(), FinalizationJob{UID: "u", ConversationID: "c"}); err == nil {
		t.Fatal("expected enqueue error")
	}
	if _, err := queue.Receive(context.Background(), time.Millisecond); err == nil {
		t.Fatal("expected receive error")
	}
	if err := queue.EnqueueMerge(context.Background(), MergeJob{UID: "u", MergedID: "m", SourceIDs: []string{"a", "b"}}); err == nil {
		t.Fatal("expected merge enqueue error")
	}
}

func TestMergeJobRejectsIncompletePayload(t *testing.T) {
	if err := (RedisQueue{}).EnqueueMerge(context.Background(), MergeJob{UID: "u", MergedID: "m"}); err == nil {
		t.Fatal("expected invalid merge job")
	}
}

func TestRedisQueueUsesDefaultKey(t *testing.T) {
	if (RedisQueue{}).key() != FinalizationQueueKey {
		t.Fatalf("unexpected default queue key")
	}
	if (RedisQueue{Key: "custom"}).key() != "custom" {
		t.Fatalf("unexpected custom queue key")
	}
}
