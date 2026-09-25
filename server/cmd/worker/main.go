package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"remi/server/internal/actionitems"
	"remi/server/internal/chat"
	"remi/server/internal/config"
	"remi/server/internal/conversations"
	"remi/server/internal/finalization"
	"remi/server/internal/framerequests"
	"remi/server/internal/integrations"
	"remi/server/internal/memories"
	"remi/server/internal/storage"
	"remi/server/internal/stt"
	"remi/server/internal/syncjobs"
	"remi/server/internal/transcripts"
)

func main() {
	cfg := config.FromEnv()
	connections, err := storage.Open(cfg.MySQLDSN, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		log.Fatalf("open worker storage: %v", err)
	}
	defer connections.Close()
	service := conversations.Service{Client: connections.Ent, DB: connections.MySQL}
	provider := stt.Provider(stt.NullProvider{})
	if cfg.STTEndpoint != "" {
		provider = stt.HTTPProvider{Endpoint: strings.TrimRight(cfg.STTEndpoint, "/") + "/v2/transcribe", APIKey: cfg.STTAPIKey}
	}
	syncQueue := syncjobs.Queue{Client: connections.Redis, Jobs: connections.Ent}
	chatProvider := chat.Provider(chat.NullProvider{})
	if cfg.LLMEndpoint != "" {
		chatProvider = chat.HTTPProvider{Endpoint: cfg.LLMEndpoint, APIKey: cfg.LLMAPIKey, Model: cfg.LLMModel}
	}
	finalizer := finalization.Processor{Conversations: service, Transcripts: transcripts.Service{Client: connections.Ent}, Memories: memories.Service{Client: connections.Ent}, ActionItems: actionitems.Service{Client: connections.Ent}, Provider: chatProvider}
	xHandler := integrations.Handler{Service: integrations.Service{Client: connections.Ent, Redis: connections.Redis}, DB: connections.MySQL}
	go runXSyncLoop(context.Background(), xHandler)
	finalQueue := conversations.RedisQueue{Client: connections.Redis}
	syncWorker := syncjobs.Worker{Queue: syncQueue, STT: provider, Conversations: service, Transcripts: transcripts.Service{Client: connections.Ent}, Finalization: finalQueue}
	frameStore, frameStoreErr := framerequests.NewGCSStore(context.Background(), os.Getenv("BUCKET_FRAME_REQUESTS"))
	if frameStoreErr != nil {
		log.Printf("GCS frame-request store unavailable; using local storage: %v", frameStoreErr)
	}
	if frameStore == nil {
		root := os.Getenv("FRAME_REQUEST_STORAGE_ROOT")
		if root == "" {
			root = "data/frame-requests"
		}
		frameStore = nil
		go framerequests.Retention{DB: connections.MySQL, Store: framerequests.LocalStore{Root: root}}.RunLoop(context.Background())
	} else {
		defer frameStore.Close()
		go framerequests.Retention{DB: connections.MySQL, Store: frameStore}.RunLoop(context.Background())
	}
	log.Printf("Remi worker listening on %s, %s, and %s", conversations.FinalizationQueueKey, conversations.MergeQueueKey, syncjobs.QueueKey)
	for {
		result, err := connections.Redis.BLPop(context.Background(), 30*time.Second, conversations.FinalizationQueueKey, conversations.MergeQueueKey, syncjobs.QueueKey).Result()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			log.Printf("receive finalization job: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if len(result) != 2 {
			log.Printf("invalid worker queue item")
			continue
		}
		if result[0] == syncjobs.QueueKey {
			job, e := syncQueue.Get(context.Background(), result[1])
			if e != nil {
				log.Printf("get sync job: %v", e)
				continue
			}
			if e := syncWorker.Process(context.Background(), job); e != nil {
				log.Printf("process sync job=%s: %v", job.ID, e)
			}
			continue
		}
		if result[0] == conversations.MergeQueueKey {
			var job conversations.MergeJob
			if e := json.Unmarshal([]byte(result[1]), &job); e != nil || job.UID == "" || job.MergedID == "" || len(job.SourceIDs) < 2 {
				log.Printf("invalid merge job")
				continue
			}
			if job.Reprocess {
				if e := finalizer.Process(context.Background(), job.UID, job.MergedID); e != nil {
					log.Printf("process merged conversation uid=%s conversation=%s: %v; keeping source conversations", job.UID, job.MergedID, e)
					continue
				}
			}
			if e := service.DeleteSources(context.Background(), job.UID, job.SourceIDs); e != nil {
				log.Printf("delete merged source conversations uid=%s merged=%s: %v", job.UID, job.MergedID, e)
			}
			continue
		}
		var job conversations.FinalizationJob
		if e := json.Unmarshal([]byte(result[1]), &job); e != nil || job.UID == "" || job.ConversationID == "" {
			log.Printf("invalid finalization job")
			continue
		}
		if err := finalizer.Process(context.Background(), job.UID, job.ConversationID); err != nil {
			log.Printf("process finalization uid=%s conversation=%s: %v", job.UID, job.ConversationID, err)
			if failErr := service.FailFinalization(context.Background(), job.UID, job.ConversationID); failErr != nil {
				log.Printf("mark finalization failed uid=%s conversation=%s: %v", job.UID, job.ConversationID, failErr)
			}
			continue
		}
		if err := service.CompleteFinalization(context.Background(), job.UID, job.ConversationID); err != nil {
			log.Printf("complete finalization uid=%s conversation=%s: %v", job.UID, job.ConversationID, err)
		}
	}
}

func runXSyncLoop(ctx context.Context, handler integrations.Handler) {
	interval := 6 * time.Hour
	if raw := os.Getenv("X_SYNC_INTERVAL_HOURS"); raw != "" {
		if hours, e := time.ParseDuration(raw + "h"); e == nil && hours >= time.Hour {
			interval = hours
		}
	}
	for {
		users, synced, posts := handler.SyncConnectedUsers(ctx)
		log.Printf("X periodic sync users=%d synced=%d new_posts=%d", users, synced, posts)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
