package account

import (
	"encoding/json"
	"errors"
	"net/http"
	"remi/server/internal/auth"
)

type Handler struct{ Service Service }

func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	uid, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	if e = h.Service.Delete(r.Context(), uid); errors.Is(e, ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if e != nil {
		http.Error(w, "Could not delete account. Please try again.", 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
