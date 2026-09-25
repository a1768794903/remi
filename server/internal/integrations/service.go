package integrations

import (
	"context"
	"errors"
	"github.com/redis/go-redis/v9"
	"remi/server/ent"
	"remi/server/ent/user"
	"strings"
	"time"
)

var ErrNotFound = errors.New("integration not found")

type Service struct {
	Client *ent.Client
	Redis  *redis.Client
}

func (s Service) owner(ctx context.Context, uid string) (*ent.User, error) {
	u, e := s.Client.User.Query().Where(user.ExternalUIDEQ(uid)).Only(ctx)
	if ent.IsNotFound(e) {
		return s.Client.User.Create().SetExternalUID(uid).SetEmail(uid).SetName(uid).Save(ctx)
	}
	return u, e
}
func clone(v map[string]any) map[string]any {
	out := map[string]any{}
	for k, x := range v {
		out[k] = x
	}
	return out
}
func (s Service) Get(ctx context.Context, uid, key string) (map[string]any, error) {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return nil, e
	}
	if key == "gmail" {
		key = "google_calendar"
	}
	data := clone(u.Integrations)
	raw, ok := data[key].(map[string]any)
	if !ok {
		return map[string]any{"connected": false, "app_key": key}, nil
	}
	if _, task := taskProvider(key); task {
		out := clone(raw)
		out["app_key"] = key
		return out, nil
	}
	connected, _ := raw["connected"].(bool)
	return map[string]any{"connected": connected, "app_key": key}, nil
}
func (s Service) Raw(ctx context.Context, uid, key string) (map[string]any, error) {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return nil, e
	}
	data := clone(u.Integrations)
	raw, _ := data[key].(map[string]any)
	if raw == nil {
		return map[string]any{}, nil
	}
	return raw, nil
}
func (s Service) Save(ctx context.Context, uid, key string, in map[string]any) error {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 128 {
		return errors.New("invalid app_key")
	}
	u, e := s.owner(ctx, uid)
	if e != nil {
		return e
	}
	data := clone(u.Integrations)
	merged := map[string]any{}
	if existing, ok := data[key].(map[string]any); ok {
		merged = clone(existing)
	}
	for k, v := range in {
		merged[k] = v
	}
	data[key] = merged
	_, e = s.Client.User.UpdateOneID(u.ID).SetIntegrations(data).Save(ctx)
	return e
}
func (s Service) Delete(ctx context.Context, uid, key string) error {
	u, e := s.owner(ctx, uid)
	if e != nil {
		return e
	}
	data := clone(u.Integrations)
	if _, ok := data[key]; !ok {
		return ErrNotFound
	}
	delete(data, key)
	_, e = s.Client.User.UpdateOneID(u.ID).SetIntegrations(data).Save(ctx)
	return e
}
func (s Service) AppleHealth(ctx context.Context, uid string, in map[string]any) (map[string]any, error) {
	health := map[string]any{"period_days": in["period_days"], "steps": map[string]any{}, "sleep": map[string]any{}, "heart_rate": map[string]any{}, "active_energy": map[string]any{}, "workouts": []any{}}
	if v := in["total_steps"]; v != nil {
		health["steps"] = map[string]any{"total": v, "average_per_day": in["average_steps_per_day"], "daily": in["daily_steps"]}
	}
	if v := in["total_sleep_hours"]; v != nil {
		health["sleep"] = map[string]any{"total_sleep_hours": v, "total_in_bed_hours": in["total_in_bed_hours"], "sessions": in["sleep_sessions"], "daily": in["daily_sleep"]}
	}
	if v := in["heart_rate_average"]; v != nil {
		health["heart_rate"] = map[string]any{"average": v, "minimum": in["heart_rate_min"], "maximum": in["heart_rate_max"]}
	}
	if v := in["total_active_energy"]; v != nil {
		health["active_energy"] = map[string]any{"total": v, "average_per_day": in["average_active_energy_per_day"], "daily": in["daily_active_energy"]}
	}
	if v := in["workouts"]; v != nil {
		health["workouts"] = v
	}
	data := map[string]any{"connected": true, "health_data": health, "last_synced": time.Now().UTC().Format(time.RFC3339Nano)}
	if e := s.Save(ctx, uid, "apple_health", data); e != nil {
		return nil, e
	}
	types := []string{"period_days"}
	for k, v := range health {
		if k != "period_days" && v != nil {
			types = append(types, k)
		}
	}
	return map[string]any{"status": "ok", "app_key": "apple_health", "synced_at": data["last_synced"], "data_types_synced": types}, nil
}
