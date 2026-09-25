package syncjobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"remi/server/ent"
	"remi/server/ent/syncjob"
	"remi/server/internal/conversations"
	"remi/server/internal/stt"
	"remi/server/internal/transcripts"
)

const QueueKey = "remi:sync-jobs"
const MaxUploadBytes int64 = 200_000_000

type File struct {
	Name string `json:"name"`
	Path string `json:"-"`
	Size int64  `json:"size"`
}
type Job struct {
	ID                  string    `json:"job_id"`
	UID                 string    `json:"uid"`
	ConversationID      string    `json:"conversation_id,omitempty"`
	Status              string    `json:"status"`
	TotalSegments       int       `json:"total_segments"`
	ProcessedSegments   int       `json:"processed_segments"`
	SuccessfulSegments  int       `json:"successful_segments"`
	FailedSegments      int       `json:"failed_segments"`
	Result              any       `json:"result,omitempty"`
	Error               string    `json:"error,omitempty"`
	Lane                string    `json:"lane"`
	ReasonCode          string    `json:"reason_code,omitempty"`
	RetryAfter          int       `json:"retry_after,omitempty"`
	RecordingAgeSeconds int       `json:"recording_age_seconds,omitempty"`
	Files               []File    `json:"-"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}
type Queue struct {
	Client  *redis.Client
	DataDir string
	Jobs    *ent.Client
}

func (q Queue) dataDir() string {
	if q.DataDir != "" {
		return q.DataDir
	}
	if v := os.Getenv("REMI_SYNC_DATA_DIR"); v != "" {
		return v
	}
	return filepath.Join("data", "sync")
}
func (q Queue) key(id string) string { return "remi:sync-job:" + id }
func (q Queue) Save(ctx context.Context, j Job) error {
	j.UpdatedAt = time.Now().UTC()
	raw, e := json.Marshal(j)
	if e != nil {
		return e
	}
	if q.Jobs != nil {
		if err := q.saveDurable(ctx, j); err != nil {
			return err
		}
	}
	if q.Client == nil {
		if q.Jobs == nil {
			return errors.New("sync queue redis client is required")
		}
		return nil
	}
	return q.Client.Set(ctx, q.key(j.ID), raw, 24*time.Hour).Err()
}
func (q Queue) Get(ctx context.Context, id string) (Job, error) {
	var j Job
	var e error
	if q.Client != nil {
		var raw []byte
		raw, e = q.Client.Get(ctx, q.key(id)).Bytes()
		if e == nil {
			e = json.Unmarshal(raw, &j)
		}
	} else {
		e = errors.New("redis sync job state unavailable")
	}
	if e != nil && q.Jobs != nil {
		node, durableErr := q.Jobs.SyncJob.Query().Where(syncjob.JobIDEQ(id)).Only(ctx)
		if durableErr != nil {
			return Job{}, e
		}
		j = fromDurable(node)
		e = nil
	}
	if e != nil {
		if q.Client == nil && q.Jobs == nil {
			return Job{}, errors.New("sync queue storage is not configured")
		}
		return Job{}, e
	}
	entries, e := os.ReadDir(filepath.Join(q.dataDir(), id))
	if e == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, statErr := entry.Info()
			if statErr == nil {
				j.Files = append(j.Files, File{Name: entry.Name(), Path: filepath.Join(q.dataDir(), id, entry.Name()), Size: info.Size()})
			}
		}
	}
	return j, nil
}

func (q Queue) saveDurable(ctx context.Context, j Job) error {
	query := q.Jobs.SyncJob.Query().Where(syncjob.JobIDEQ(j.ID))
	node, err := query.Only(ctx)
	var builder *ent.SyncJobCreate
	if ent.IsNotFound(err) {
		builder = q.Jobs.SyncJob.Create().SetJobID(j.ID).SetUID(j.UID).SetStatus(j.Status).SetLane(j.Lane)
		if j.ConversationID != "" {
			builder.SetConversationID(j.ConversationID)
		}
		builder.SetTotalSegments(j.TotalSegments).SetProcessedSegments(j.ProcessedSegments).SetSuccessfulSegments(j.SuccessfulSegments).SetFailedSegments(j.FailedSegments)
	} else if err != nil {
		return err
	} else {
		update := q.Jobs.SyncJob.UpdateOneID(node.ID).SetUID(j.UID).SetStatus(j.Status).SetLane(j.Lane).SetTotalSegments(j.TotalSegments).SetProcessedSegments(j.ProcessedSegments).SetSuccessfulSegments(j.SuccessfulSegments).SetFailedSegments(j.FailedSegments)
		if j.ConversationID != "" {
			update.SetConversationID(j.ConversationID)
		}
		if j.ReasonCode != "" {
			update.SetReasonCode(j.ReasonCode)
		} else {
			update.ClearReasonCode()
		}
		if j.Error != "" {
			update.SetError(j.Error)
		} else {
			update.ClearError()
		}
		if j.RetryAfter != 0 {
			update.SetRetryAfter(j.RetryAfter)
		} else {
			update.ClearRetryAfter()
		}
		if j.RecordingAgeSeconds != 0 {
			update.SetRecordingAgeSeconds(j.RecordingAgeSeconds)
		} else {
			update.ClearRecordingAgeSeconds()
		}
		if result, ok := j.Result.(map[string]any); ok {
			update.SetResult(result)
		}
		_, err = update.Save(ctx)
		return err
	}
	if result, ok := j.Result.(map[string]any); ok {
		builder.SetResult(result)
	}
	if j.ReasonCode != "" {
		builder.SetReasonCode(j.ReasonCode)
	}
	if j.Error != "" {
		builder.SetError(j.Error)
	}
	if j.RetryAfter != 0 {
		builder.SetRetryAfter(j.RetryAfter)
	}
	if j.RecordingAgeSeconds != 0 {
		builder.SetRecordingAgeSeconds(j.RecordingAgeSeconds)
	}
	_, err = builder.Save(ctx)
	return err
}

func fromDurable(node *ent.SyncJob) Job {
	return Job{ID: node.JobID, UID: node.UID, ConversationID: node.ConversationID, Status: node.Status, TotalSegments: node.TotalSegments, ProcessedSegments: node.ProcessedSegments, SuccessfulSegments: node.SuccessfulSegments, FailedSegments: node.FailedSegments, Lane: node.Lane, ReasonCode: node.ReasonCode, RetryAfter: node.RetryAfter, RecordingAgeSeconds: node.RecordingAgeSeconds, Error: node.Error, Result: node.Result, CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt}
}
func (q Queue) Enqueue(ctx context.Context, j Job) error {
	if e := q.Save(ctx, j); e != nil {
		return e
	}
	return q.Client.RPush(ctx, QueueKey, j.ID).Err()
}
func (q Queue) Receive(ctx context.Context, timeout time.Duration) (Job, error) {
	if q.Client == nil {
		return Job{}, errors.New("sync queue redis client is required")
	}
	v, e := q.Client.BLPop(ctx, timeout, QueueKey).Result()
	if e != nil {
		return Job{}, e
	}
	if len(v) < 2 {
		return Job{}, errors.New("invalid sync queue item")
	}
	return q.Get(ctx, v[1])
}
func (q Queue) WriteFiles(id string, parts []multipartPart) ([]File, error) {
	dir := filepath.Join(q.dataDir(), id)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	files := make([]File, 0, len(parts))
	for _, p := range parts {
		name := filepath.Base(p.Name)
		if name == "." || name == "" {
			return nil, errors.New("invalid file name")
		}
		path := filepath.Join(dir, name)
		if e := os.WriteFile(path, p.Data, 0600); e != nil {
			return nil, e
		}
		files = append(files, File{Name: name, Path: path, Size: int64(len(p.Data))})
	}
	return files, nil
}
func (q Queue) RemoveFiles(id string) { _ = os.RemoveAll(filepath.Join(q.dataDir(), id)) }

type multipartPart struct {
	Name string
	Data []byte
}

func NewJob(uid, conversationID string, files []File) Job {
	return Job{ID: uuid.NewString(), UID: uid, ConversationID: conversationID, Status: "queued", Lane: "fresh", Files: files, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
}

type Worker struct {
	Queue         Queue
	STT           stt.Provider
	Conversations conversations.Service
	Transcripts   transcripts.Service
	Finalization  conversations.Enqueuer
}

func (w Worker) Process(ctx context.Context, j Job) error {
	j.Status = "processing"
	_ = w.Queue.Save(ctx, j)
	defer w.Queue.RemoveFiles(j.ID)
	if len(j.Files) == 0 {
		return w.fail(ctx, j, "no_files")
	}
	for _, f := range j.Files {
		raw, e := os.ReadFile(f.Path)
		if e != nil {
			j.FailedSegments++
			continue
		}
		codec := codecFromName(f.Name)
		result, e := w.STT.Transcribe(ctx, stt.Request{Audio: raw, Codec: codec})
		if e != nil {
			j.FailedSegments++
			j.Error = "stt_unavailable"
			continue
		}
		if j.ConversationID == "" {
			item, e := w.Conversations.Create(ctx, j.UID, conversations.CreateInput{Title: "Synced recording", Summary: "", StartedAt: nil})
			if e != nil {
				return w.fail(ctx, j, "conversation_create_failed")
			}
			j.ConversationID = item.ID
		}
		for _, seg := range result.Segments {
			_, e = w.Transcripts.Create(ctx, j.UID, j.ConversationID, transcripts.CreateInput{Speaker: seg.Speaker, Text: seg.Text, StartMs: int64(seg.StartSec * 1000), EndMs: int64(seg.EndSec * 1000), Source: "sync"})
			j.TotalSegments++
			j.ProcessedSegments++
			if e != nil {
				j.FailedSegments++
			} else {
				j.SuccessfulSegments++
			}
		}
	}
	if j.FailedSegments > 0 {
		j.Status = "failed"
		if j.Error == "" {
			j.Error = "partial_processing_failure"
		}
	} else {
		j.Status = "completed"
	}
	if err := w.Queue.Save(ctx, j); err != nil {
		return err
	}
	if w.Finalization != nil && j.ConversationID != "" && j.Status == "completed" {
		if err := w.Finalization.EnqueueFinalization(ctx, conversations.FinalizationJob{UID: j.UID, ConversationID: j.ConversationID}); err != nil {
			return err
		}
	}
	return nil
}
func (w Worker) fail(ctx context.Context, j Job, reason string) error {
	j.Status = "failed"
	j.Error = reason
	return w.Queue.Save(ctx, j)
}
func codecFromName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "pcm16"):
		return "pcm16"
	case strings.Contains(lower, "pcm8"):
		return "pcm8"
	case strings.Contains(lower, "opus"):
		return "opus"
	default:
		return "opus"
	}
}
func (q Queue) String() string {
	if q.Client == nil {
		return fmt.Sprintf("%s(unconfigured)", QueueKey)
	}
	return QueueKey
}
