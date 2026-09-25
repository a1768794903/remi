package actionitems

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"remi/server/ent"
	"remi/server/ent/actionitem"
	"remi/server/ent/user"
)

var ErrNotFound = errors.New("action item not found")

type Item struct {
	ID              string     `json:"id"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	Completed       bool       `json:"completed"`
	Owner           string     `json:"owner"`
	Source          string     `json:"source"`
	DueAt           *time.Time `json:"due_at,omitempty"`
	Exported        bool       `json:"exported"`
	ExportPlatform  *string    `json:"export_platform,omitempty"`
	AppleReminderID *string    `json:"apple_reminder_id,omitempty"`
	IsLocked        bool       `json:"is_locked"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type CreateInput struct {
	Description    string     `json:"description"`
	Status         string     `json:"status"`
	Owner          string     `json:"owner"`
	Source         string     `json:"source"`
	DueAt          *time.Time `json:"due_at"`
	ConversationID *string    `json:"conversation_id"`
}

type UpdateInput struct {
	Description     *string     `json:"description"`
	Status          *string     `json:"status"`
	Completed       *bool       `json:"completed"`
	Owner           *string     `json:"owner"`
	Source          *string     `json:"source"`
	DueAt           **time.Time `json:"due_at"`
	Exported        *bool       `json:"exported"`
	ExportPlatform  *string     `json:"export_platform"`
	AppleReminderID *string     `json:"apple_reminder_id"`
}

type BatchSyncInput struct {
	ID              string      `json:"id"`
	Description     *string     `json:"description"`
	Completed       *bool       `json:"completed"`
	DueAt           **time.Time `json:"due_at"`
	Exported        *bool       `json:"exported"`
	ExportPlatform  *string     `json:"export_platform"`
	AppleReminderID *string     `json:"apple_reminder_id"`
}

type Service struct {
	Client *ent.Client
	Redis  *redis.Client
}

func (s Service) Update(ctx context.Context, uid, id string, in UpdateInput) (Item, error) {
	current, err := s.Get(ctx, uid, id)
	if err != nil {
		return Item{}, err
	}
	if current.IsLocked {
		return Item{}, errors.New("action item is locked")
	}
	numericID, _ := strconv.Atoi(current.ID)
	update := s.Client.ActionItem.UpdateOneID(numericID)
	if in.Description != nil {
		update.SetDescription(*in.Description)
	}
	if in.Owner != nil {
		update.SetOwner(actionitem.Owner(*in.Owner))
	}
	if in.Source != nil {
		update.SetSource(*in.Source)
	}
	if in.Status != nil {
		update.SetStatus(actionitem.Status(*in.Status))
		if *in.Status == string(actionitem.StatusCompleted) {
			update.SetCompletedAt(time.Now().UTC())
		} else {
			update.ClearCompletedAt()
		}
	}
	if in.Completed != nil {
		if *in.Completed {
			update.SetStatus(actionitem.StatusCompleted).SetCompletedAt(time.Now().UTC())
		} else {
			update.SetStatus(actionitem.StatusActive).ClearCompletedAt()
		}
	}
	if in.DueAt != nil {
		if *in.DueAt == nil {
			update.ClearDueAt()
		} else {
			update.SetDueAt(**in.DueAt)
		}
	}
	if in.Exported != nil {
		update.SetExported(*in.Exported)
	}
	if in.ExportPlatform != nil {
		update.SetExportPlatform(*in.ExportPlatform)
	}
	if in.AppleReminderID != nil {
		update.SetAppleReminderID(*in.AppleReminderID)
	}
	item, err := update.Save(ctx)
	if ent.IsNotFound(err) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	return toItem(item), nil
}

func (s Service) user(ctx context.Context, uid string) (*ent.User, error) {
	if s.Client == nil {
		return nil, errors.New("ent client is not configured")
	}
	return s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
}

func (s Service) EnsureUser(ctx context.Context, uid string) (*ent.User, error) {
	existing, err := s.user(ctx, uid)
	if err == nil {
		return existing, nil
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}
	return s.Client.User.Create().SetExternalUID(uid).SetEmail(uid).SetName(uid).Save(ctx)
}

func (s Service) Create(ctx context.Context, uid string, in CreateInput) (Item, error) {
	u, err := s.EnsureUser(ctx, uid)
	if err != nil {
		return Item{}, err
	}
	status := actionitem.Status(in.Status)
	if status == "" {
		status = actionitem.StatusActive
	}
	owner := actionitem.Owner(in.Owner)
	if owner == "" {
		owner = actionitem.OwnerUser
	}
	source := in.Source
	if source == "" {
		source = "manual"
	}
	builder := s.Client.ActionItem.Create().SetDescription(in.Description).SetStatus(status).SetOwner(owner).SetSource(source).SetUserID(u.ID)
	if in.DueAt != nil {
		builder.SetDueAt(*in.DueAt)
	}
	if in.ConversationID != nil {
		if id, parseErr := strconv.Atoi(*in.ConversationID); parseErr == nil {
			builder.SetConversationID(id)
		}
	}
	item, err := builder.Save(ctx)
	if err != nil {
		return Item{}, err
	}
	return toItem(item), nil
}

func (s Service) List(ctx context.Context, uid string, limit, offset int, completed *bool) ([]Item, error) {
	u, err := s.user(ctx, uid)
	if ent.IsNotFound(err) {
		return []Item{}, nil
	}
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	query := s.Client.ActionItem.Query().Where(actionitem.HasUserWith(user.IDEQ(u.ID))).Order(ent.Desc(actionitem.FieldCreatedAt)).Limit(limit).Offset(offset)
	if completed != nil {
		if *completed {
			query = query.Where(actionitem.StatusEQ(actionitem.StatusCompleted))
		} else {
			query = query.Where(actionitem.StatusNEQ(actionitem.StatusCompleted))
		}
	}
	items, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Item, 0, len(items))
	for _, item := range items {
		result = append(result, toItem(item))
	}
	return result, nil
}

func (s Service) Get(ctx context.Context, uid, id string) (Item, error) {
	u, err := s.user(ctx, uid)
	if err != nil {
		return Item{}, ErrNotFound
	}
	numericID, err := strconv.Atoi(id)
	if err != nil {
		return Item{}, ErrNotFound
	}
	item, err := s.Client.ActionItem.Query().Where(actionitem.IDEQ(numericID), actionitem.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	if ent.IsNotFound(err) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	return toItem(item), nil
}

func (s Service) Delete(ctx context.Context, uid, id string) error {
	item, err := s.Get(ctx, uid, id)
	if err != nil {
		return err
	}
	numericID, _ := strconv.Atoi(item.ID)
	if err := s.Client.ActionItem.DeleteOneID(numericID).Exec(ctx); err != nil {
		return err
	}
	return nil
}

func (s Service) BatchCreate(ctx context.Context, uid string, inputs []CreateInput) ([]Item, error) {
	result := make([]Item, 0, len(inputs))
	for _, input := range inputs {
		item, err := s.Create(ctx, uid, input)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s Service) BatchDelete(ctx context.Context, uid string, ids []string) (int, error) {
	deleted := 0
	for _, id := range ids {
		if err := s.Delete(ctx, uid, id); err == nil {
			deleted++
		} else if !errors.Is(err, ErrNotFound) {
			return deleted, err
		}
	}
	return deleted, nil
}

func (s Service) BatchSync(ctx context.Context, uid string, inputs []BatchSyncInput) (int, error) {
	updated := 0
	for _, input := range inputs {
		patch := UpdateInput{Description: input.Description, Completed: input.Completed, DueAt: input.DueAt, Exported: input.Exported, ExportPlatform: input.ExportPlatform, AppleReminderID: input.AppleReminderID}
		item, err := s.Update(ctx, uid, input.ID, patch)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return updated, err
		}
		_ = item
		updated++
	}
	return updated, nil
}

func (s Service) PendingSync(ctx context.Context, uid string) (pending []Item, synced []Item, err error) {
	u, err := s.user(ctx, uid)
	if ent.IsNotFound(err) {
		return []Item{}, []Item{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	base := s.Client.ActionItem.Query().Where(actionitem.HasUserWith(user.IDEQ(u.ID)), actionitem.IsLockedEQ(false))
	pendingNodes, err := base.Clone().Where(actionitem.ExportedEQ(false)).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	syncedNodes, err := base.Clone().Where(actionitem.ExportedEQ(true), actionitem.AppleReminderIDNotNil()).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, node := range pendingNodes {
		pending = append(pending, toItem(node))
	}
	for _, node := range syncedNodes {
		synced = append(synced, toItem(node))
	}
	return pending, synced, nil
}

func (s Service) Search(ctx context.Context, uid, text string, limit int) ([]Item, error) {
	u, err := s.user(ctx, uid)
	if ent.IsNotFound(err) {
		return []Item{}, nil
	}
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	nodes, err := s.Client.ActionItem.Query().Where(actionitem.HasUserWith(user.IDEQ(u.ID)), actionitem.DescriptionContainsFold(text), actionitem.IsLockedEQ(false)).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(nodes))
	for _, node := range nodes {
		items = append(items, toItem(node))
	}
	return items, nil
}

func (s Service) IDs(ctx context.Context, uid string, completed *bool) ([]string, error) {
	items, err := s.List(ctx, uid, 500, 0, completed)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

func toItem(item *ent.ActionItem) Item {
	return Item{ID: strconv.Itoa(item.ID), Description: item.Description, Status: string(item.Status), Completed: item.Status == actionitem.StatusCompleted, Owner: string(item.Owner), Source: item.Source, DueAt: item.DueAt, Exported: item.Exported, ExportPlatform: item.ExportPlatform, AppleReminderID: item.AppleReminderID, IsLocked: item.IsLocked, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}
