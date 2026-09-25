package goals

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"remi/server/ent"
	"remi/server/ent/goal"
	"remi/server/ent/goalprogressevent"
	"remi/server/ent/user"
	"strconv"
	"strings"
	"time"
)

var ErrNotFound = errors.New("goal not found")

type Service struct{ Client *ent.Client }

func (s Service) owner(ctx context.Context, uid string) (*ent.User, error) {
	u, e := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(e) {
		return s.Client.User.Create().SetExternalUID(uid).SetEmail(uid).SetName(uid).Save(ctx)
	}
	return u, e
}
func metricValue(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
func response(n *ent.Goal, latest int) map[string]any {
	m := metricValue(n.Metric)
	typ, _ := m["type"].(string)
	cur, _ := m["current"].(float64)
	target, _ := m["target"].(float64)
	min, _ := m["min"].(float64)
	max, _ := m["max"].(float64)
	unit, _ := m["unit"].(string)
	active := n.Status == goal.StatusBackground || n.Status == goal.StatusFocused
	return map[string]any{"id": n.ExternalID, "goal_id": n.ExternalID, "title": n.Title, "desired_outcome": n.DesiredOutcome, "why_it_matters": n.WhyItMatters, "success_criteria": n.SuccessCriteria, "horizon_at": n.HorizonAt, "status": string(n.Status), "focus_rank": n.FocusRank, "metric": m, "source": string(n.Source), "created_at": n.CreatedAt, "updated_at": n.UpdatedAt, "ended_at": n.EndedAt, "latest_progress_sequence": latest, "goal_type": typ, "target_value": target, "current_value": cur, "min_value": min, "max_value": max, "unit": unit, "is_active": active}
}
func (s Service) latest(ctx context.Context, n *ent.Goal) int {
	v, e := n.QueryProgressEvents().Order(ent.Desc(goalprogressevent.FieldSequence)).First(ctx)
	if e != nil {
		return 0
	}
	return v.Sequence
}
func (s Service) Create(ctx context.Context, uid string, in map[string]any) (map[string]any, error) {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return nil, e
	}
	title, _ := in["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 500 {
		return nil, errors.New("invalid title")
	}
	desired, _ := in["desired_outcome"].(string)
	if desired == "" {
		desired = title
	}
	status, _ := in["status"].(string)
	if status == "" {
		status = "background"
	}
	source, _ := in["source"].(string)
	if source == "" {
		source = "user"
	}
	b := s.Client.Goal.Create().SetExternalID("goal_" + uuid.NewString()).SetTitle(title).SetDesiredOutcome(desired).SetStatus(goal.Status(status)).SetSource(goal.Source(source)).SetUserID(u.ID)
	if v, ok := in["why_it_matters"].(string); ok {
		b.SetWhyItMatters(v)
	}
	if v, ok := in["success_criteria"].([]any); ok {
		a := make([]string, 0, len(v))
		for _, x := range v {
			if z, ok := x.(string); ok {
				a = append(a, z)
			}
		}
		b.SetSuccessCriteria(a)
	}
	if v, ok := in["metric"].(map[string]any); ok {
		b.SetMetric(v)
	}
	n, e := b.Save(ctx)
	if e != nil {
		return nil, e
	}
	return response(n, 0), nil
}
func (s Service) List(ctx context.Context, uid string, ended bool) ([]map[string]any, error) {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return nil, e
	}
	q := s.Client.Goal.Query().Where(goal.HasUserWith(user.IDEQ(u.ID))).Order(ent.Desc(goal.FieldUpdatedAt))
	if !ended {
		q = q.Where(goal.StatusIn(goal.StatusBackground, goal.StatusFocused, goal.StatusPaused))
	}
	ns, e := q.All(ctx)
	if e != nil {
		return nil, e
	}
	out := make([]map[string]any, 0, len(ns))
	for _, n := range ns {
		out = append(out, response(n, s.latest(ctx, n)))
	}
	return out, nil
}
func (s Service) Get(ctx context.Context, uid, id string) (*ent.Goal, error) {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return nil, e
	}
	n, e := s.Client.Goal.Query().Where(goal.ExternalIDEQ(id), goal.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	if ent.IsNotFound(e) {
		return nil, ErrNotFound
	}
	return n, e
}
func (s Service) Update(ctx context.Context, uid, id string, in map[string]any) (map[string]any, error) {
	n, e := s.Get(ctx, uid, id)
	if e != nil {
		return nil, e
	}
	b := s.Client.Goal.UpdateOneID(n.ID)
	if v, ok := in["title"].(string); ok {
		b.SetTitle(strings.TrimSpace(v))
	}
	if v, ok := in["desired_outcome"].(string); ok {
		b.SetDesiredOutcome(v)
	}
	if v, ok := in["why_it_matters"].(string); ok {
		b.SetWhyItMatters(v)
	}
	if v, ok := in["status"].(string); ok {
		b.SetStatus(goal.Status(v))
	}
	if v, ok := in["focus_rank"].(float64); ok {
		b.SetFocusRank(int(v))
	}
	if v, ok := in["metric"].(map[string]any); ok {
		b.SetMetric(v)
	}
	if in["clear_metric"] == true {
		b.ClearMetric()
	}
	n, e = b.Save(ctx)
	if e != nil {
		return nil, e
	}
	return response(n, s.latest(ctx, n)), nil
}
func (s Service) Delete(ctx context.Context, uid, id string) error {
	n, e := s.Get(ctx, uid, id)
	if e != nil {
		return e
	}
	_, e = s.Client.Goal.UpdateOneID(n.ID).SetStatus(goal.StatusAbandoned).SetEndedAt(time.Now()).Save(ctx)
	return e
}
func (s Service) Progress(ctx context.Context, uid, id string, current float64) (map[string]any, error) {
	n, e := s.Get(ctx, uid, id)
	if e != nil {
		return nil, e
	}
	m := metricValue(n.Metric)
	m["current"] = current
	nn, e := s.Client.Goal.UpdateOneID(n.ID).SetMetric(m).Save(ctx)
	if e != nil {
		return nil, e
	}
	return response(nn, s.latest(ctx, nn)), nil
}
func (s Service) AppendEvent(ctx context.Context, uid, id string, in map[string]any) (map[string]any, error) {
	n, e := s.Get(ctx, uid, id)
	if e != nil {
		return nil, e
	}
	kind, _ := in["kind"].(string)
	summary, _ := in["summary"].(string)
	if kind == "" || summary == "" {
		return nil, errors.New("kind and summary are required")
	}
	seq := s.latest(ctx, n) + 1
	ev, e := s.Client.GoalProgressEvent.Create().SetExternalID(uuid.NewString()).SetSequence(seq).SetKind(goalprogressevent.Kind(kind)).SetSummary(summary).SetGoalID(n.ID).Save(ctx)
	if e != nil {
		return nil, e
	}
	return map[string]any{"event_id": ev.ExternalID, "goal_id": id, "sequence": ev.Sequence, "kind": string(ev.Kind), "summary": ev.Summary, "evidence_refs": ev.EvidenceRefs, "metric": ev.Metric, "created_at": ev.CreatedAt}, nil
}
func (s Service) Events(ctx context.Context, uid, id string, limit int) ([]map[string]any, error) {
	n, e := s.Get(ctx, uid, id)
	if e != nil {
		return nil, e
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	es, e := n.QueryProgressEvents().Order(ent.Asc(goalprogressevent.FieldSequence)).Limit(limit).All(ctx)
	if e != nil {
		return nil, e
	}
	out := make([]map[string]any, 0, len(es))
	for _, ev := range es {
		out = append(out, map[string]any{"event_id": ev.ExternalID, "goal_id": id, "sequence": ev.Sequence, "kind": string(ev.Kind), "summary": ev.Summary, "evidence_refs": ev.EvidenceRefs, "metric": ev.Metric, "created_at": ev.CreatedAt})
	}
	return out, nil
}
func parseID(v string) int { n, _ := strconv.Atoi(v); return n }
