package actionitems

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const taskShareTTL = 30 * 24 * time.Hour

func taskShareKey(token string) string    { return "task_share:" + token }
func taskAcceptedKey(token string) string { return taskShareKey(token) + ":accepted" }

type taskShare struct {
	UID         string   `json:"uid"`
	DisplayName string   `json:"display_name"`
	TaskIDs     []string `json:"task_ids"`
}

func (s Service) Share(ctx context.Context, uid string, ids []string) (string, error) {
	if s.Redis == nil {
		return "", errors.New("sharing is not configured")
	}
	if len(ids) == 0 || len(ids) > 20 {
		return "", errors.New("between 1 and 20 task IDs are required")
	}
	for _, id := range ids {
		item, e := s.Get(ctx, uid, id)
		if e != nil {
			return "", ErrNotFound
		}
		if item.IsLocked {
			return "", errors.New("cannot share locked action items")
		}
	}
	u, e := s.EnsureUser(ctx, uid)
	if e != nil {
		return "", e
	}
	token := uuid.NewString()
	raw, _ := json.Marshal(taskShare{UID: uid, DisplayName: u.Name, TaskIDs: ids})
	if e = s.Redis.Set(ctx, taskShareKey(token), raw, taskShareTTL).Err(); e != nil {
		return "", e
	}
	return token, nil
}
func (s Service) ReadShare(ctx context.Context, token string) (taskShare, error) {
	if s.Redis == nil {
		return taskShare{}, errors.New("sharing is not configured")
	}
	raw, e := s.Redis.Get(ctx, taskShareKey(token)).Bytes()
	if e != nil {
		return taskShare{}, e
	}
	var share taskShare
	if json.Unmarshal(raw, &share) != nil || share.UID == "" {
		return taskShare{}, errors.New("invalid share")
	}
	return share, nil
}
func (s Service) SharedPreview(ctx context.Context, share taskShare) ([]Item, error) {
	out := []Item{}
	for _, id := range share.TaskIDs {
		item, e := s.Get(ctx, share.UID, id)
		if e == nil && !item.IsLocked {
			out = append(out, item)
		}
	}
	return out, nil
}
func (s Service) AcceptShare(ctx context.Context, uid, token string) ([]string, error) {
	share, e := s.ReadShare(ctx, token)
	if e != nil {
		return nil, e
	}
	if share.UID == uid {
		return nil, errors.New("cannot accept your own shared tasks")
	}
	if ok, e := s.Redis.SAdd(ctx, taskAcceptedKey(token), uid).Result(); e != nil {
		return nil, e
	} else if ok != 1 {
		return nil, errors.New("share already accepted")
	}
	created := []string{}
	for _, id := range share.TaskIDs {
		item, e := s.Get(ctx, share.UID, id)
		if e != nil || item.IsLocked {
			continue
		}
		copyItem, e := s.Create(ctx, uid, CreateInput{Description: item.Description, Status: "active", Owner: "user", Source: "shared", DueAt: item.DueAt})
		if e != nil {
			_ = s.Redis.SRem(ctx, taskAcceptedKey(token), uid).Err()
			return nil, e
		}
		created = append(created, copyItem.ID)
	}
	_ = s.Redis.Expire(ctx, taskAcceptedKey(token), taskShareTTL).Err()
	if len(created) == 0 {
		_ = s.Redis.SRem(ctx, taskAcceptedKey(token), uid).Err()
		return nil, errors.New("shared tasks are no longer available")
	}
	return created, nil
}
