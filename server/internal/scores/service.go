package scores

import (
	"context"
	"errors"
	"math"
	"time"

	"remi/server/ent"
	"remi/server/ent/actionitem"
	"remi/server/ent/user"
)

type Period struct {
	Score          float64 `json:"score"`
	CompletedTasks int     `json:"completed_tasks"`
	TotalTasks     int     `json:"total_tasks"`
}

type DailyScore struct {
	Date           string `json:"date"`
	Score          int    `json:"score"`
	CompletedTasks int    `json:"completed_tasks"`
	TotalTasks     int    `json:"total_tasks"`
}

type Scores struct {
	Daily      Period `json:"daily"`
	Weekly     Period `json:"weekly"`
	Overall    Period `json:"overall"`
	DefaultTab string `json:"default_tab"`
	Date       string `json:"date"`
}

type Service struct{ Client *ent.Client }

func scorePeriod(completed, total int) Period {
	if total <= 0 {
		return Period{}
	}
	return Period{Score: math.Round(float64(completed)/float64(total)*1000) / 10, CompletedTasks: completed, TotalTasks: total}
}

func parseDate(value string, location *time.Location) (time.Time, error) {
	if value == "" {
		now := time.Now().In(location)
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location), nil
	}
	return time.ParseInLocation("2006-01-02", value, location)
}

func (s Service) items(ctx context.Context, uid string) ([]*ent.ActionItem, *time.Location, error) {
	u, err := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(err) {
		return []*ent.ActionItem{}, time.UTC, nil
	}
	if err != nil {
		return nil, nil, err
	}
	location := time.UTC
	if u.TimeZone != "" {
		if loaded, loadErr := time.LoadLocation(u.TimeZone); loadErr == nil {
			location = loaded
		}
	}
	items, err := s.Client.ActionItem.Query().Where(actionitem.HasUserWith(user.IDEQ(u.ID))).All(ctx)
	return items, location, err
}

func (s Service) Daily(ctx context.Context, uid, date string) (DailyScore, error) {
	items, location, err := s.items(ctx, uid)
	if err != nil {
		return DailyScore{}, err
	}
	day, err := parseDate(date, location)
	if err != nil {
		return DailyScore{}, errors.New("date must use YYYY-MM-DD")
	}
	end := day.AddDate(0, 0, 1)
	completed, total := 0, 0
	for _, item := range items {
		if item.DueAt == nil {
			continue
		}
		due := item.DueAt.In(location)
		if !due.Before(day) && due.Before(end) {
			total++
			if item.Status == actionitem.StatusCompleted {
				completed++
			}
		}
	}
	period := scorePeriod(completed, total)
	return DailyScore{Date: day.Format("2006-01-02"), Score: int(math.Round(period.Score)), CompletedTasks: completed, TotalTasks: total}, nil
}

func (s Service) All(ctx context.Context, uid, date string) (Scores, error) {
	items, location, err := s.items(ctx, uid)
	if err != nil {
		return Scores{}, err
	}
	day, err := parseDate(date, location)
	if err != nil {
		return Scores{}, errors.New("date must use YYYY-MM-DD")
	}
	end := day.AddDate(0, 0, 1)
	weekStart := day.AddDate(0, 0, -6)
	dailyDone, dailyTotal, weeklyDone, weeklyTotal, overallDone, overallTotal := 0, 0, 0, 0, 0, 0
	for _, item := range items {
		if item.Status == actionitem.StatusCancelled || item.Status == actionitem.StatusSuperseded {
			continue
		}
		overallTotal++
		if item.Status == actionitem.StatusCompleted {
			overallDone++
		}
		created := item.CreatedAt.In(location)
		if !created.Before(weekStart) && created.Before(end) {
			weeklyTotal++
			if item.Status == actionitem.StatusCompleted {
				weeklyDone++
			}
		}
		if item.DueAt != nil {
			due := item.DueAt.In(location)
			if !due.Before(day) && due.Before(end) {
				dailyTotal++
				if item.Status == actionitem.StatusCompleted {
					dailyDone++
				}
			}
		}
	}
	daily := scorePeriod(dailyDone, dailyTotal)
	weekly := scorePeriod(weeklyDone, weeklyTotal)
	overall := scorePeriod(overallDone, overallTotal)
	defaultTab := "overall"
	if dailyTotal > 0 {
		defaultTab = "daily"
	} else if weeklyTotal > 0 {
		defaultTab = "weekly"
	}
	return Scores{Daily: daily, Weekly: weekly, Overall: overall, DefaultTab: defaultTab, Date: day.Format("2006-01-02")}, nil
}
