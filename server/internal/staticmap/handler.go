package staticmap

import (
	"net/http"
	"strconv"
)

type Handler struct{ Service Service }

func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	pins, err := parsePins(r.URL.Query().Get("pins"))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w0, e0 := strconv.Atoi(r.URL.Query().Get("width"))
	h0, e1 := strconv.Atoi(r.URL.Query().Get("height"))
	if e0 != nil || e1 != nil || w0 < 64 || w0 > 1280 || h0 < 64 || h0 > 1280 {
		http.Error(w, "width and height must be between 64 and 1280", 400)
		return
	}
	body, err := h.Service.Fetch(r.Context(), pins, w0, h0)
	if err != nil {
		http.Error(w, "static map is temporarily unavailable", 502)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(body)
}
