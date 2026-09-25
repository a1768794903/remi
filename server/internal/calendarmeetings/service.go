package calendarmeetings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"remi/server/ent"
	"remi/server/ent/calendarmeeting"
	"remi/server/ent/user"
)

type Participant struct {
	Name  *string `json:"name,omitempty"`
	Email *string `json:"email,omitempty"`
}
type Input struct {
	CalendarEventID string           `json:"calendar_event_id"`
	CalendarSource  string           `json:"calendar_source"`
	Title           string           `json:"title"`
	StartTime       time.Time        `json:"start_time"`
	EndTime         time.Time        `json:"end_time"`
	Platform        *string          `json:"platform"`
	MeetingLink     *string          `json:"meeting_link"`
	Participants    []map[string]any `json:"participants"`
	Notes           *string          `json:"notes"`
}
type Context struct {
	ID              string           `json:"id,omitempty"`
	CalendarEventID string           `json:"calendar_event_id"`
	Title           string           `json:"title"`
	Participants    []map[string]any `json:"participants"`
	Platform        *string          `json:"platform,omitempty"`
	MeetingLink     *string          `json:"meeting_link,omitempty"`
	StartTime       time.Time        `json:"start_time"`
	DurationMinutes int              `json:"duration_minutes"`
	Notes           *string          `json:"notes,omitempty"`
	CalendarSource  string           `json:"calendar_source"`
}

type Service struct{ Client *ent.Client }

func naturalKey(uid, source, event string) string {
	sum := sha256.Sum256([]byte(uid + "\x00" + source + "\x00" + event))
	return hex.EncodeToString(sum[:])
}
func durationMinutes(start, end time.Time) int { return int(end.Sub(start).Minutes()) }

func (s Service) owner(ctx context.Context, uid string) (*ent.User, error) {
	u, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return s.Client.User.Create().SetExternalUID(uid).SetEmail(uid).SetName(uid).Save(ctx)
	}
	return u, err
}

func (s Service) Store(ctx context.Context, uid string, in Input) (Context, error) {
	if strings.TrimSpace(in.CalendarEventID) == "" || strings.TrimSpace(in.Title) == "" || in.EndTime.Before(in.StartTime) {
		return Context{}, errors.New("invalid calendar meeting")
	}
	if in.CalendarSource == "" {
		in.CalendarSource = "system_calendar"
	}
	u, err := s.owner(ctx, uid)
	if err != nil {
		return Context{}, err
	}
	key := naturalKey(uid, in.CalendarSource, in.CalendarEventID)
	now := time.Now().UTC()
	q, findErr := s.Client.CalendarMeeting.Query().Where(calendarmeeting.ExternalIDEQ(key)).Only(ctx)
	var node *ent.CalendarMeeting
	if ent.IsNotFound(findErr) {
		b := s.Client.CalendarMeeting.Create().SetExternalID(key).SetCalendarEventID(in.CalendarEventID).SetCalendarSource(in.CalendarSource).SetTitle(in.Title).SetStartTime(in.StartTime.UTC()).SetEndTime(in.EndTime.UTC()).SetDurationMinutes(durationMinutes(in.StartTime, in.EndTime)).SetSyncedAt(now).SetUserID(u.ID)
		if in.Participants != nil {
			b.SetParticipants(in.Participants)
		}
		if in.Platform != nil {
			b.SetPlatform(*in.Platform)
		}
		if in.MeetingLink != nil {
			b.SetMeetingLink(*in.MeetingLink)
		}
		if in.Notes != nil {
			b.SetNotes(*in.Notes)
		}
		node, err = b.Save(ctx)
	} else if findErr == nil {
		node = q
		up := s.Client.CalendarMeeting.UpdateOneID(q.ID).SetCalendarEventID(in.CalendarEventID).SetCalendarSource(in.CalendarSource).SetTitle(in.Title).SetStartTime(in.StartTime.UTC()).SetEndTime(in.EndTime.UTC()).SetDurationMinutes(durationMinutes(in.StartTime, in.EndTime)).SetSyncedAt(now)
		if in.Participants != nil {
			up.SetParticipants(in.Participants)
		}
		if in.Platform != nil {
			up.SetPlatform(*in.Platform)
		}
		if in.MeetingLink != nil {
			up.SetMeetingLink(*in.MeetingLink)
		}
		if in.Notes != nil {
			up.SetNotes(*in.Notes)
		}
		node, err = up.Save(ctx)
	} else {
		err = findErr
	}
	if err != nil {
		return Context{}, err
	}
	return toContext(node), nil
}

func (s Service) Get(ctx context.Context, uid, id string) (Context, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return Context{}, err
	}
	var node *ent.CalendarMeeting
	if n, e := strconv.Atoi(id); e == nil {
		node, err = s.Client.CalendarMeeting.Query().Where(calendarmeeting.IDEQ(n), calendarmeeting.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	} else {
		node, err = s.Client.CalendarMeeting.Query().Where(calendarmeeting.ExternalIDEQ(id), calendarmeeting.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	}
	if ent.IsNotFound(err) {
		return Context{}, errors.New("meeting not found")
	}
	return toContext(node), err
}

func (s Service) List(ctx context.Context, uid string, start, end *time.Time, limit int) ([]Context, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := s.Client.CalendarMeeting.Query().Where(calendarmeeting.HasUserWith(user.IDEQ(u.ID))).Order(ent.Desc(calendarmeeting.FieldStartTime)).Limit(limit)
	if start != nil {
		q = q.Where(calendarmeeting.StartTimeGTE(start.UTC()))
	}
	if end != nil {
		q = q.Where(calendarmeeting.StartTimeLTE(end.UTC()))
	}
	nodes, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Context, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, toContext(n))
	}
	return out, nil
}

func toContext(n *ent.CalendarMeeting) Context {
	return Context{ID: n.ExternalID, CalendarEventID: n.CalendarEventID, Title: n.Title, Participants: n.Participants, Platform: n.Platform, MeetingLink: n.MeetingLink, StartTime: n.StartTime, DurationMinutes: n.DurationMinutes, Notes: n.Notes, CalendarSource: n.CalendarSource}
}
