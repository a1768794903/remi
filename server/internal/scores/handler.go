package scores

import (
	"encoding/json"
	"net/http"
	"remi/server/internal/auth"
)

type Handler struct{ Service Service }

func (h Handler) Daily(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	value, err := h.Service.Daily(r.Context(), uid, r.URL.Query().Get("date"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

func (h Handler) All(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	value, err := h.Service.All(r.Context(), uid, r.URL.Query().Get("date"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}
