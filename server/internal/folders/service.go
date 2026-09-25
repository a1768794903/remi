package folders

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"remi/server/ent"
	"remi/server/ent/conversation"
	"remi/server/ent/folder"
	"remi/server/ent/user"
)

var ErrNotFound = errors.New("folder not found")

type Item struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Description       *string `json:"description,omitempty"`
	Color             string  `json:"color"`
	Icon              string  `json:"icon"`
	Order             int     `json:"order"`
	IsDefault         bool    `json:"is_default"`
	IsSystem          bool    `json:"is_system"`
	CategoryMapping   *string `json:"category_mapping,omitempty"`
	ConversationCount int     `json:"conversation_count"`
}
type Service struct{ Client *ent.Client }

func (s Service) owner(ctx context.Context, uid string) (*ent.User, error) {
	u, e := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(e) {
		return s.Client.User.Create().SetExternalUID(uid).SetEmail(uid).SetName(uid).Save(ctx)
	}
	return u, e
}
func toItem(n *ent.Folder, count int) Item {
	return Item{ID: n.ExternalID, Name: n.Name, Description: n.Description, Color: n.Color, Icon: n.Icon, Order: n.Order, IsDefault: n.IsDefault, IsSystem: n.IsSystem, CategoryMapping: n.CategoryMapping, ConversationCount: count}
}
func (s Service) List(ctx context.Context, uid string) ([]Item, error) {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return nil, e
	}
	q := s.Client.Folder.Query().Where(folder.HasUserWith(user.IDEQ(u.ID))).Order(ent.Asc(folder.FieldOrder))
	ns, e := q.All(ctx)
	if e != nil {
		return nil, e
	}
	out := make([]Item, 0, len(ns))
	for _, n := range ns {
		c, _ := n.QueryConversations().Count(ctx)
		out = append(out, toItem(n, c))
	}
	if len(out) == 0 {
		return s.init(ctx, u)
	}
	return out, nil
}
func (s Service) init(ctx context.Context, u *ent.User) ([]Item, error) {
	defs := []struct{ name, cat, icon, color, desc string }{{"Work", "work", "💼", "#3B82F6", "Work, business, professional, and career-related conversations"}, {"Personal", "personal", "👤", "#10B981", "Personal life, family, health, hobbies, and self-improvement"}, {"Social", "social", "👥", "#8B5CF6", "Friends, social gatherings, entertainment, and casual conversations"}}
	out := make([]Item, 0, 3)
	for i, d := range defs {
		n, e := s.Client.Folder.Create().SetExternalID(uuid.NewString()).SetName(d.name).SetCategoryMapping(d.cat).SetIcon(d.icon).SetColor(d.color).SetDescription(d.desc).SetOrder(i).SetIsSystem(true).SetUserID(u.ID).Save(ctx)
		if e != nil {
			return nil, e
		}
		out = append(out, toItem(n, 0))
	}
	return out, nil
}
func (s Service) Get(ctx context.Context, uid, id string) (*ent.Folder, error) {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return nil, e
	}
	n, e := s.Client.Folder.Query().Where(folder.ExternalIDEQ(id), folder.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	if ent.IsNotFound(e) {
		return nil, ErrNotFound
	}
	return n, e
}
func (s Service) Create(ctx context.Context, uid, name, desc, color, icon string) (Item, error) {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return Item{}, e
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return Item{}, errors.New("invalid folder name")
	}
	if color == "" {
		color = "#6B7280"
	}
	if icon == "" {
		icon = "📁"
	}
	order, _ := s.Client.Folder.Query().Where(folder.HasUserWith(user.IDEQ(u.ID))).Count(ctx)
	n, e := s.Client.Folder.Create().SetExternalID(uuid.NewString()).SetName(name).SetNillableDescription(&desc).SetColor(color).SetIcon(icon).SetOrder(order).SetUserID(u.ID).Save(ctx)
	if e != nil {
		return Item{}, e
	}
	return toItem(n, 0), nil
}
func (s Service) Update(ctx context.Context, uid, id string, in map[string]any) (Item, error) {
	n, e := s.Get(ctx, uid, id)
	if e != nil {
		return Item{}, e
	}
	b := s.Client.Folder.UpdateOneID(n.ID)
	if v, ok := in["name"].(string); ok {
		b.SetName(strings.TrimSpace(v))
	}
	if v, ok := in["description"].(string); ok {
		b.SetDescription(v)
	}
	if v, ok := in["color"].(string); ok {
		b.SetColor(v)
	}
	if v, ok := in["icon"].(string); ok {
		b.SetIcon(v)
	}
	if v, ok := in["order"].(float64); ok {
		b.SetOrder(int(v))
	}
	n, e = b.Save(ctx)
	if e != nil {
		return Item{}, e
	}
	c, _ := n.QueryConversations().Count(ctx)
	return toItem(n, c), nil
}
func (s Service) Delete(ctx context.Context, uid, id string) error {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return e
	}
	n, e := s.Get(ctx, uid, id)
	if e != nil {
		return e
	}
	if n.IsSystem {
		return errors.New("cannot delete system folder")
	}
	_, e = s.Client.Conversation.Update().Where(conversation.FolderID(n.ID), conversation.HasUserWith(user.IDEQ(u.ID))).ClearFolderID().Save(ctx)
	if e != nil {
		return e
	}
	return s.Client.Folder.DeleteOneID(n.ID).Exec(ctx)
}
func (s Service) Move(ctx context.Context, uid, conversationID, folderID string) error {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return e
	}
	cid, er := strconv.Atoi(conversationID)
	if er != nil {
		return errors.New("conversation not found")
	}
	q := s.Client.Conversation.Query().Where(conversation.IDEQ(cid), conversation.HasUserWith(user.IDEQ(u.ID)))
	if folderID == "" {
		n, queryErr := q.Only(ctx)
		if queryErr == nil {
			_, e = s.Client.Conversation.UpdateOneID(n.ID).ClearFolderID().Save(ctx)
		} else {
			e = queryErr
		}
		return e
	}
	f, e := s.Get(ctx, uid, folderID)
	if e != nil {
		return e
	}
	n, queryErr := q.Only(ctx)
	if queryErr == nil {
		_, e = s.Client.Conversation.UpdateOneID(n.ID).SetFolderID(f.ID).Save(ctx)
	} else {
		e = queryErr
	}
	return e
}
