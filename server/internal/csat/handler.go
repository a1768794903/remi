package csat

import (
	"encoding/json"
	"net/http"
	"remi/server/internal/auth"
)

type Handler struct{ Service Service }

func (h Handler) Config(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(h.Service.Config(r.Context()))
}
func (h Handler) Rating(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var in struct {
		Platform   string `json:"platform"`
		AppVersion string `json:"app_version"`
		Score      int    `json:"score"`
		Comment    string `json:"comment"`
		Revision   int    `json:"revision"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	id, created, err := h.Service.Submit(r.Context(), uid, in.Platform, in.AppVersion, in.Comment, in.Score, in.Revision)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !created {
		w.WriteHeader(http.StatusConflict)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "created": created})
}
