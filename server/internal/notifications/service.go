package notifications

import (
	"context"
	"errors"
	"strings"

	"remi/server/ent"
	"remi/server/ent/notificationtoken"
	"remi/server/ent/user"
)

var ErrUserNotFound = errors.New("user not found")

type Service struct{ Client *ent.Client }

type DailySummarySettings struct {
	Enabled bool `json:"enabled"`
	Hour    int  `json:"hour"`
}

type MentorSettings struct {
	Frequency int `json:"frequency"`
}

func validateDailySummaryHour(hour int) error {
	if hour < 0 || hour > 23 {
		return errors.New("hour must be between 0 and 23")
	}
	return nil
}

func validateMentorFrequency(frequency int) error {
	if frequency < 0 || frequency > 5 {
		return errors.New("frequency must be between 0 and 5")
	}
	return nil
}

func (s Service) owner(ctx context.Context, uid string) (*ent.User, error) {
	n, e := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(e) {
		return s.Client.User.Create().SetExternalUID(uid).SetEmail(uid).SetName(uid).Save(ctx)
	}
	return n, e
}
func (s Service) SaveToken(ctx context.Context, uid, token, platform, deviceKey string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("token is required")
	}
	u, e := s.owner(ctx, uid)
	if e != nil {
		return e
	}
	if platform == "" {
		platform = "unknown"
	}
	if deviceKey == "" {
		deviceKey = "default"
	}
	n, e := s.Client.NotificationToken.Query().Where(notificationtoken.DeviceKeyEQ(deviceKey), notificationtoken.HasUserWith(user.IDEQ(u.ID))).Only(ctx)
	if ent.IsNotFound(e) {
		_, e = s.Client.NotificationToken.Create().SetToken(token).SetPlatform(platform).SetDeviceKey(deviceKey).SetUserID(u.ID).Save(ctx)
		return e
	}
	if e != nil {
		return e
	}
	_, e = s.Client.NotificationToken.UpdateOneID(n.ID).SetToken(token).SetPlatform(platform).Save(ctx)
	return e
}
func (s Service) SetTimeZone(ctx context.Context, uid, zone string) error {
	zone = strings.TrimSpace(zone)
	if zone == "" || len(zone) > 128 {
		return errors.New("invalid time_zone")
	}
	u, e := s.owner(ctx, uid)
	if e != nil {
		return e
	}
	_, e = s.Client.User.UpdateOneID(u.ID).SetTimeZone(zone).Save(ctx)
	return e
}

func (s Service) DailySummarySettings(ctx context.Context, uid string) (DailySummarySettings, error) {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return DailySummarySettings{}, e
	}
	return DailySummarySettings{Enabled: u.DailySummaryEnabled, Hour: u.DailySummaryHourLocal}, nil
}

func (s Service) UpdateDailySummarySettings(ctx context.Context, uid string, enabled *bool, hour *int) error {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return e
	}
	if hour != nil {
		if err := validateDailySummaryHour(*hour); err != nil {
			return err
		}
	}
	b := s.Client.User.UpdateOneID(u.ID)
	if enabled != nil {
		b.SetDailySummaryEnabled(*enabled)
	}
	if hour != nil {
		b.SetDailySummaryHourLocal(*hour)
	}
	_, e = b.Save(ctx)
	return e
}

func (s Service) MentorSettings(ctx context.Context, uid string) (MentorSettings, error) {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return MentorSettings{}, e
	}
	return MentorSettings{Frequency: u.MentorNotificationFrequency}, nil
}

func (s Service) UpdateMentorSettings(ctx context.Context, uid string, frequency int) error {
	if err := validateMentorFrequency(frequency); err != nil {
		return err
	}
	u, e := s.owner(ctx, uid)
	if e != nil {
		return e
	}
	_, e = s.Client.User.UpdateOneID(u.ID).SetMentorNotificationFrequency(frequency).Save(ctx)
	return e
}
