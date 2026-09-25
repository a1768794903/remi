package conversations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"remi/server/ent"
	"remi/server/ent/conversation"
	"remi/server/ent/transcriptsegment"
	"remi/server/ent/user"
)

var ErrNotFound = errors.New("conversation not found")

type Item struct {
	ID                string           `json:"id"`
	Title             string           `json:"title"`
	Summary           string           `json:"summary"`
	Visibility        string           `json:"visibility"`
	Starred           bool             `json:"starred"`
	Status            string           `json:"status"`
	StartedAt         time.Time        `json:"started_at"`
	EndedAt           *time.Time       `json:"ended_at,omitempty"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	AudioFiles        []map[string]any `json:"audio_files,omitempty"`
	ConversationAudio map[string]any   `json:"conversation_audio,omitempty"`
	Photos            []map[string]any `json:"photos,omitempty"`
}

type CreateInput struct {
	Title     string     `json:"title"`
	Summary   string     `json:"summary"`
	StartedAt *time.Time `json:"started_at"`
}

type UpdateInput struct {
	Title      *string          `json:"title"`
	Summary    *string          `json:"summary"`
	Visibility *string          `json:"visibility"`
	Starred    *bool            `json:"starred"`
	AudioFiles []map[string]any `json:"audio_files"`
}

type FinalizationStatus struct {
	ConversationID string `json:"conversation_id"`
	Status         string `json:"status"`
}

type Service struct {
	Client *ent.Client
	DB     *sql.DB
}

func (s Service) StartSession(ctx context.Context, uid, deviceID string, started time.Time) (string, error) {
	item, err := s.Create(ctx, uid, CreateInput{Title: "", Summary: "", StartedAt: &started})
	if err != nil {
		return "", err
	}
	return item.ID, nil
}

func (s Service) FinishSession(ctx context.Context, uid, id string, ended time.Time) error {
	item, err := s.Get(ctx, uid, id)
	if err != nil {
		return err
	}
	numericID, _ := strconv.Atoi(item.ID)
	_, err = s.Client.Conversation.UpdateOneID(numericID).SetEndedAt(ended).SetStatus(conversation.StatusCompleted).Save(ctx)
	return err
}

func (s Service) ensureUser(ctx context.Context, uid string) (*ent.User, error) {
	userNode, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if err == nil {
		return userNode, nil
	}
	if !ent.IsNotFound(err) {
		return nil, err
	}
	return s.Client.User.Create().SetExternalUID(uid).SetEmail(uid).SetName(uid).Save(ctx)
}

func (s Service) Create(ctx context.Context, uid string, input CreateInput) (Item, error) {
	u, err := s.ensureUser(ctx, uid)
	if err != nil {
		return Item{}, err
	}
	started := time.Now().UTC()
	if input.StartedAt != nil {
		started = input.StartedAt.UTC()
	}
	node, err := s.Client.Conversation.Create().SetUserID(u.ID).SetTitle(input.Title).SetSummary(input.Summary).SetStartedAt(started).SetStatus(conversation.StatusInProgress).Save(ctx)
	if err != nil {
		return Item{}, err
	}
	return s.withPhotos(ctx, uid, toItem(node)), nil
}

func (s Service) List(ctx context.Context, uid string, limit, offset int) ([]Item, error) {
	u, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return []Item{}, nil
	}
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	nodes, err := s.Client.Conversation.Query().Where(conversation.HasUserWith(user.IDEQ(u.ID))).Order(ent.Desc(conversation.FieldStartedAt)).Limit(limit).Offset(offset).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Item, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, s.withPhotos(ctx, uid, toItem(node)))
	}
	return result, nil
}

func (s Service) Count(ctx context.Context, uid string, statuses []string) (int, error) {
	u, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	query := s.Client.Conversation.Query().Where(conversation.HasUserWith(user.IDEQ(u.ID)))
	if len(statuses) > 0 {
		allowed := make([]conversation.Status, 0, len(statuses))
		for _, value := range statuses {
			status := conversation.Status(value)
			if status != conversation.StatusInProgress && status != conversation.StatusProcessing && status != conversation.StatusMerging && status != conversation.StatusCompleted && status != conversation.StatusFailed {
				continue
			}
			allowed = append(allowed, status)
		}
		if len(allowed) > 0 {
			query = query.Where(conversation.StatusIn(allowed...))
		}
	}
	return query.Count(ctx)
}

func (s Service) Finalize(ctx context.Context, uid, id string, ended time.Time) (Item, error) {
	item, err := s.Get(ctx, uid, id)
	if err != nil {
		return Item{}, err
	}
	numericID, _ := strconv.Atoi(item.ID)
	update := s.Client.Conversation.UpdateOneID(numericID)
	if item.Status == string(conversation.StatusInProgress) || item.Status == string(conversation.StatusProcessing) || item.Status == string(conversation.StatusMerging) {
		update.SetStatus(conversation.StatusProcessing).SetEndedAt(ended)
	}
	node, err := update.Save(ctx)
	if ent.IsNotFound(err) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	return s.withPhotos(ctx, uid, toItem(node)), nil
}

func (s Service) Finalization(ctx context.Context, uid, id string) (FinalizationStatus, error) {
	item, err := s.Get(ctx, uid, id)
	if err != nil {
		return FinalizationStatus{}, err
	}
	status := "completed"
	if item.Status == string(conversation.StatusFailed) {
		status = "failed"
	}
	if item.Status == string(conversation.StatusInProgress) || item.Status == string(conversation.StatusProcessing) || item.Status == string(conversation.StatusMerging) {
		status = "pending"
	}
	return FinalizationStatus{ConversationID: id, Status: status}, nil
}

func (s Service) CompleteFinalization(ctx context.Context, uid, id string) error {
	item, err := s.Get(ctx, uid, id)
	if err != nil {
		return err
	}
	if item.Status != string(conversation.StatusProcessing) && item.Status != string(conversation.StatusMerging) {
		return nil
	}
	numericID, _ := strconv.Atoi(item.ID)
	_, err = s.Client.Conversation.UpdateOneID(numericID).SetStatus(conversation.StatusCompleted).Save(ctx)
	return err
}

func (s Service) FailFinalization(ctx context.Context, uid, id string) error {
	item, err := s.Get(ctx, uid, id)
	if err != nil {
		return err
	}
	numericID, _ := strconv.Atoi(item.ID)
	_, err = s.Client.Conversation.UpdateOneID(numericID).SetStatus(conversation.StatusFailed).Save(ctx)
	return err
}

func (s Service) Search(ctx context.Context, uid, query string, limit int) ([]Item, error) {
	u, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return []Item{}, nil
	}
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	nodes, err := s.Client.Conversation.Query().Where(
		conversation.HasUserWith(user.IDEQ(u.ID)),
		conversation.Or(conversation.TitleContainsFold(query), conversation.SummaryContainsFold(query)),
	).Order(ent.Desc(conversation.FieldStartedAt)).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, s.withPhotos(ctx, uid, toItem(node)))
	}
	return out, nil
}

func (s Service) Get(ctx context.Context, uid, id string) (Item, error) {
	u, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if err != nil {
		return Item{}, ErrNotFound
	}
	numericID, err := strconv.Atoi(id)
	if err != nil {
		return Item{}, ErrNotFound
	}
	node, err := s.Client.Conversation.Query().Where(conversation.IDEQ(numericID), conversation.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	if ent.IsNotFound(err) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	return s.withPhotos(ctx, uid, toItem(node)), nil
}

func (s Service) Update(ctx context.Context, uid, id string, input UpdateInput) (Item, error) {
	current, err := s.Get(ctx, uid, id)
	if err != nil {
		return Item{}, err
	}
	numericID, _ := strconv.Atoi(current.ID)
	update := s.Client.Conversation.UpdateOneID(numericID)
	if input.Title != nil {
		update.SetTitle(*input.Title)
	}
	if input.Summary != nil {
		update.SetSummary(*input.Summary)
	}
	if input.Visibility != nil {
		update.SetVisibility(*input.Visibility)
	}
	if input.Starred != nil {
		update.SetStarred(*input.Starred)
	}
	if input.AudioFiles != nil {
		update.SetAudioFiles(input.AudioFiles)
	}
	node, err := update.Save(ctx)
	if ent.IsNotFound(err) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	return s.withPhotos(ctx, uid, toItem(node)), nil
}

func (s Service) Delete(ctx context.Context, uid, id string) error {
	item, err := s.Get(ctx, uid, id)
	if err != nil {
		return err
	}
	numericID, _ := strconv.Atoi(item.ID)
	if err := s.Client.Conversation.DeleteOneID(numericID).Exec(ctx); err != nil {
		return err
	}
	return nil
}

// DeleteSources removes the source conversations after a merge job has been
// processed. Ownership is checked for every ID so a queued job can never
// delete another user's data.
func (s Service) DeleteSources(ctx context.Context, uid string, ids []string) error {
	for _, id := range ids {
		if err := s.Delete(ctx, uid, id); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return nil
}

// Merge copies owned source conversations into one canonical conversation in
// a single Ent transaction. Segment timestamps are shifted relative to the
// earliest source start, preserving gaps between recordings.
func (s Service) Merge(ctx context.Context, uid string, ids []string) (Item, error) {
	if len(ids) < 2 {
		return Item{}, errors.New("at least 2 conversations are required")
	}
	u, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if err != nil {
		return Item{}, ErrNotFound
	}
	tx, err := s.Client.Tx(ctx)
	if err != nil {
		return Item{}, err
	}
	rollback := func(e error) (Item, error) { _ = tx.Rollback(); return Item{}, e }
	sources := make([]*ent.Conversation, 0, len(ids))
	seen := map[int]bool{}
	for _, raw := range ids {
		id, e := strconv.Atoi(raw)
		if e != nil {
			return rollback(ErrNotFound)
		}
		if seen[id] {
			return rollback(errors.New("duplicate conversation id"))
		}
		seen[id] = true
		node, e := tx.Conversation.Query().Where(conversation.IDEQ(id), conversation.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
		if ent.IsNotFound(e) {
			return rollback(ErrNotFound)
		}
		if e != nil {
			return rollback(e)
		}
		if node.Status == conversation.StatusMerging {
			return rollback(errors.New("conversation is already merging"))
		}
		sources = append(sources, node)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].StartedAt.Before(sources[j].StartedAt) })
	started := sources[0].StartedAt
	ended := started
	title := "Merged conversation"
	summaries := make([]string, 0, len(sources))
	for _, src := range sources {
		if src.EndedAt != nil && src.EndedAt.After(ended) {
			ended = *src.EndedAt
		}
		if strings.TrimSpace(src.Title) != "" && title == "Merged conversation" {
			title = src.Title
		}
		if strings.TrimSpace(src.Summary) != "" {
			summaries = append(summaries, src.Summary)
		}
	}
	merged, err := tx.Conversation.Create().SetUserID(u.ID).SetTitle(title).SetSummary(strings.Join(summaries, "\n\n")).SetStartedAt(started).SetEndedAt(ended).SetStatus(conversation.StatusCompleted).Save(ctx)
	if err != nil {
		return rollback(err)
	}
	for _, src := range sources {
		offset := src.StartedAt.Sub(started).Milliseconds()
		segments, e := tx.TranscriptSegment.Query().Where(transcriptsegment.HasConversationWith(conversation.IDEQ(src.ID))).Order(ent.Asc(transcriptsegment.FieldStartMs)).All(ctx)
		if e != nil {
			return rollback(e)
		}
		for _, seg := range segments {
			b := tx.TranscriptSegment.Create().SetConversationID(merged.ID).SetSpeaker(seg.Speaker).SetSpeakerID(seg.SpeakerID).SetIsUser(seg.IsUser).SetText(seg.Text).SetStartMs(offset + seg.StartMs).SetEndMs(offset + seg.EndMs).SetSource(seg.Source)
			if seg.PersonID != nil {
				b.SetPersonID(*seg.PersonID)
			}
			if _, e = b.Save(ctx); e != nil {
				return rollback(e)
			}
		}
		if _, e = tx.Conversation.UpdateOneID(src.ID).SetStatus(conversation.StatusMerging).Save(ctx); e != nil {
			return rollback(e)
		}
	}
	if err = tx.Commit(); err != nil {
		return Item{}, err
	}
	return s.withPhotos(ctx, uid, toItem(merged)), nil
}

func toItem(node *ent.Conversation) Item {
	return Item{ID: strconv.Itoa(node.ID), Title: node.Title, Summary: node.Summary, Visibility: node.Visibility, Starred: node.Starred, Status: string(node.Status), StartedAt: node.StartedAt, EndedAt: node.EndedAt, CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt, AudioFiles: node.AudioFiles, ConversationAudio: node.ConversationAudio}
}

func (s Service) withPhotos(ctx context.Context, uid string, item Item) Item {
	if s.DB == nil {
		return item
	}
	var raw []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT photos FROM conversations c JOIN users u ON u.id=c.user_id WHERE c.id=? AND u.external_uid=?`, item.ID, uid).Scan(&raw); err != nil || len(raw) == 0 {
		return item
	}
	_ = json.Unmarshal(raw, &item.Photos)
	return item
}
