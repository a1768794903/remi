package notifications

import (
	"encoding/json"
	"net/http"

	"remi/server/internal/auth"
)

type Handler struct{ Service Service }

func (h Handler) Token(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var in struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	device := r.Header.Get("X-Device-Id-Hash")
	if e = h.Service.SaveToken(r.Context(), uid, in.Token, in.Platform, device); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "Ok"})
}
func (h Handler) TimeZone(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var in struct {
		TimeZone string `json:"time_zone"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if e = h.Service.SetTimeZone(r.Context(), uid, in.TimeZone); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "Ok"})
}

func (h Handler) DailySummarySettings(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	if r.Method == http.MethodPatch {
		var in struct {
			Enabled *bool `json:"enabled"`
			Hour    *int  `json:"hour"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if e = h.Service.UpdateDailySummarySettings(r.Context(), uid, in.Enabled, in.Hour); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	value, e := h.Service.DailySummarySettings(r.Context(), uid)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func (h Handler) MentorSettings(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	if r.Method == http.MethodPatch {
		var in struct {
			Frequency *int `json:"frequency"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Frequency == nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if e = h.Service.UpdateMentorSettings(r.Context(), uid, *in.Frequency); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}
	value, e := h.Service.MentorSettings(r.Context(), uid)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}
