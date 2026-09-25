package chatfiles

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"remi/server/internal/auth"
)

type Handler struct{ Service Service }

func (h Handler) Upload(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "multipart form required", 400)
		return
	}
	files := []File{}
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			http.Error(w, "invalid multipart body", 400)
			return
		}
		if part.FormName() != "files" {
			continue
		}
		name := filepath.Base(part.FileName())
		if name == "." || name == "" {
			http.Error(w, "file name is required", 400)
			return
		}
		data, e := io.ReadAll(io.LimitReader(part, MaxPartSize+1))
		if e != nil {
			http.Error(w, "could not read file", 400)
			return
		}
		mime := part.Header.Get("Content-Type")
		if mime == "" {
			mime = "application/octet-stream"
		}
		file, e := h.Service.Upload(r.Context(), uid, name, mime, data)
		if e != nil {
			status := 400
			if strings.Contains(e.Error(), "not configured") || strings.Contains(e.Error(), "provider") {
				status = 503
			}
			http.Error(w, e.Error(), status)
			return
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		http.Error(w, "at least one file is required", 400)
		return
	}
	_ = json.NewEncoder(w).Encode(files)
}
