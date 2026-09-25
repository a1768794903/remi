package desktopusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"remi/server/internal/auth"
)

type Service struct{ DB *sql.DB }
type input struct {
	Date       string `json:"date"`
	Timezone   string `json:"timezone"`
	Device     string `json:"client_device_id"`
	Watching   int64  `json:"watching_seconds"`
	Listening  int64  `json:"listening_seconds"`
	CardsShown int64  `json:"proactive_cards_shown"`
	CardsActed int64  `json:"proactive_cards_acted"`
	PTT        int64  `json:"ptt_turns"`
}

func validate(date, zone, device string) error {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil || parsed.Format("2006-01-02") != date {
		return errors.New("date must be a real date in YYYY-MM-DD format")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return errors.New("timezone must be a valid IANA timezone")
	}
	if strings.TrimSpace(device) == "" || len(device) > 200 {
		return errors.New("client_device_id cannot be blank")
	}
	today := time.Now().In(loc)
	delta := parsed.Sub(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc))
	if delta < -48*time.Hour || delta > 48*time.Hour {
		return errors.New("date must be within 2 days of today in the supplied timezone")
	}
	return nil
}
func (s Service) Upsert(ctx context.Context, uid string, in input) error {
	if s.DB == nil {
		return sql.ErrConnDone
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO desktop_daily_usage (user_external_uid,usage_date,timezone_name,client_device_id,watching_seconds,listening_seconds,proactive_cards_shown,proactive_cards_acted,ptt_turns,created_at,updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE timezone_name=VALUES(timezone_name), watching_seconds=GREATEST(watching_seconds,VALUES(watching_seconds)), listening_seconds=GREATEST(listening_seconds,VALUES(listening_seconds)), proactive_cards_shown=GREATEST(proactive_cards_shown,VALUES(proactive_cards_shown)), proactive_cards_acted=GREATEST(proactive_cards_acted,VALUES(proactive_cards_acted)), ptt_turns=GREATEST(ptt_turns,VALUES(ptt_turns)), updated_at=UTC_TIMESTAMP(6)", uid, in.Date, in.Timezone, in.Device, in.Watching, in.Listening, in.CardsShown, in.CardsActed, in.PTT)
	return err
}

type Handler struct{ Service Service }

func (h Handler) Record(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		writeError(w, 401, err.Error())
		return
	}
	var in input
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeError(w, 400, "invalid JSON")
		return
	}
	if in.Watching < 0 || in.Watching > 86400 || in.Listening < 0 || in.Listening > 86400 || in.CardsShown < 0 || in.CardsShown > 10000 || in.CardsActed < 0 || in.CardsActed > 10000 || in.PTT < 0 || in.PTT > 10000 {
		writeError(w, 400, "invalid usage counters")
		return
	}
	if err := validate(in.Date, in.Timezone, in.Device); err != nil {
		writeError(w, 422, err.Error())
		return
	}
	if err := h.Service.Upsert(r.Context(), uid, in); err != nil {
		writeError(w, 500, "failed to record desktop usage")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"detail": msg})
}
