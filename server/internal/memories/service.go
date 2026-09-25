package memories

import (
	"context"
	"errors"
	"strconv"
	"time"

	"remi/server/ent"
	"remi/server/ent/memory"
	"remi/server/ent/user"
)

var ErrNotFound = errors.New("memory not found")

type Item struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Category    string     `json:"category"`
	Visibility  string     `json:"visibility"`
	Tags        []string   `json:"tags,omitempty"`
	IsRead      bool       `json:"is_read"`
	IsDismissed bool       `json:"is_dismissed"`
	Content     string     `json:"content"`
	Importance  int        `json:"importance"`
	EventTime   *time.Time `json:"event_time,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
type CreateInput struct {
	Type           string     `json:"type"`
	Content        string     `json:"content"`
	Importance     *int       `json:"importance"`
	EventTime      *time.Time `json:"event_time"`
	Category       string     `json:"category"`
	Visibility     string     `json:"visibility"`
	Tags           []string   `json:"tags"`
	ConversationID *string    `json:"conversation_id"`
}
type UpdateInput struct {
	Content     *string `json:"content"`
	Importance  *int    `json:"importance"`
	Visibility  *string `json:"visibility"`
	IsRead      *bool   `json:"is_read"`
	IsDismissed *bool   `json:"is_dismissed"`
}
type SearchResult struct {
	Items  []Item `json:"memories"`
	Total  int    `json:"total"`
	Source string `json:"source"`
}
type Service struct{ Client *ent.Client }

func (s Service) owner(ctx context.Context, uid string) (*ent.User, error) {
	u, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return u, err
}
func (s Service) Create(ctx context.Context, uid string, input CreateInput) (Item, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return Item{}, err
	}
	t := memory.Type(input.Type)
	if t == "" {
		t = memory.TypeFact
	}
	if !validType(t) {
		return Item{}, errors.New("invalid memory type")
	}
	b := s.Client.Memory.Create().SetUserID(u.ID).SetType(t).SetContent(input.Content)
	if input.Category != "" {
		b.SetCategory(input.Category)
	}
	if input.Visibility != "" {
		b.SetVisibility(input.Visibility)
	}
	if input.Tags != nil {
		b.SetTags(input.Tags)
	}
	if input.Importance != nil {
		b.SetImportance(*input.Importance)
	}
	if input.EventTime != nil {
		b.SetEventTime(input.EventTime.UTC())
	}
	if input.ConversationID != nil {
		if id, parseErr := strconv.Atoi(*input.ConversationID); parseErr == nil {
			b.SetConversationID(id)
		}
	}
	n, err := b.Save(ctx)
	if err != nil {
		return Item{}, err
	}
	return toItem(n), nil
}
func (s Service) List(ctx context.Context, uid string, limit, offset int) ([]Item, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return []Item{}, nil
		}
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	nodes, err := s.Client.Memory.Query().Where(memory.HasUserWith(user.IDEQ(u.ID))).Order(ent.Desc(memory.FieldCreatedAt)).Limit(limit).Offset(max(offset, 0)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, toItem(n))
	}
	return out, nil
}
func (s Service) Get(ctx context.Context, uid, id string) (Item, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return Item{}, ErrNotFound
	}
	n, err := s.Client.Memory.Query().Where(memory.IDEQ(mustID(id)), memory.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	if ent.IsNotFound(err) || err != nil {
		return Item{}, ErrNotFound
	}
	return toItem(n), nil
}
func (s Service) Update(ctx context.Context, uid, id string, input UpdateInput) (Item, error) {
	item, err := s.Get(ctx, uid, id)
	if err != nil {
		return Item{}, err
	}
	b := s.Client.Memory.UpdateOneID(mustID(item.ID))
	if input.Content != nil {
		b.SetContent(*input.Content)
	}
	if input.Importance != nil {
		b.SetImportance(*input.Importance)
	}
	if input.Visibility != nil {
		b.SetVisibility(*input.Visibility)
	}
	if input.IsRead != nil {
		b.SetIsRead(*input.IsRead)
	}
	if input.IsDismissed != nil {
		b.SetIsDismissed(*input.IsDismissed)
	}
	n, err := b.Save(ctx)
	if ent.IsNotFound(err) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	return toItem(n), nil
}
func (s Service) Delete(ctx context.Context, uid, id string) error {
	item, err := s.Get(ctx, uid, id)
	if err != nil {
		return err
	}
	return s.Client.Memory.DeleteOneID(mustID(item.ID)).Exec(ctx)
}

func (s Service) DeleteBatch(ctx context.Context, uid string, ids []string) (int, error) {
	deleted := 0
	for _, id := range ids {
		if err := s.Delete(ctx, uid, id); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return deleted, err
		} else {
			deleted++
		}
	}
	return deleted, nil
}

func (s Service) DeleteAll(ctx context.Context, uid string) (int, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return s.Client.Memory.Delete().Where(memory.HasUserWith(user.IDEQ(u.ID))).Exec(ctx)
}

func (s Service) Search(ctx context.Context, uid, query string, limit, offset int, includeArchive bool) (SearchResult, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return SearchResult{Items: []Item{}, Source: "mysql_lexical"}, nil
		}
		return SearchResult{}, err
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	q := s.Client.Memory.Query().Where(memory.HasUserWith(user.IDEQ(u.ID)), memory.ContentContainsFold(query))
	if !includeArchive {
		q = q.Where(memory.VisibilityNEQ("archive"))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	nodes, err := q.Order(ent.Desc(memory.FieldCreatedAt)).Limit(limit).Offset(offset).All(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	items := make([]Item, 0, len(nodes))
	for _, node := range nodes {
		items = append(items, toItem(node))
	}
	return SearchResult{Items: items, Total: total, Source: "mysql_lexical"}, nil
}
func toItem(n *ent.Memory) Item {
	return Item{ID: strconv.Itoa(n.ID), Type: string(n.Type), Category: n.Category, Visibility: n.Visibility, Tags: n.Tags, IsRead: n.IsRead, IsDismissed: n.IsDismissed, Content: n.Content, Importance: n.Importance, EventTime: n.EventTime, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt}
}
func validType(t memory.Type) bool {
	return t == memory.TypeFact || t == memory.TypeDecision || t == memory.TypePreference || t == memory.TypeEvent
}
func mustID(value string) int { id, _ := strconv.Atoi(value); return id }
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
