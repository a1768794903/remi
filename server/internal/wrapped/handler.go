package wrapped

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/auth"
)

type Handler struct{ DB *sql.DB }

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		writeError(w, 401, err.Error())
		return
	}
	year, err := routeYear(r.URL.Path)
	if err != nil || year != 2025 {
		writeError(w, 400, "Only year 2025 is currently supported")
		return
	}
	if h.DB == nil {
		writeError(w, 503, "wrapped storage is not configured")
		return
	}
	if r.Method == http.MethodGet {
		h.status(w, r, uid, year)
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/generate") {
		h.generate(w, r, uid, year)
		return
	}
	writeError(w, 405, "method not allowed")
}

func (h Handler) status(w http.ResponseWriter, r *http.Request, uid string, year int) {
	var status, result, errText, progress []byte
	err := h.DB.QueryRowContext(r.Context(), `SELECT status,COALESCE(result,JSON_OBJECT()),COALESCE(error,''),COALESCE(progress,JSON_OBJECT()) FROM wrapped WHERE user_external_uid=? AND year=?`, uid, year).Scan(&status, &result, &errText, &progress)
	if err == sql.ErrNoRows {
		writeJSON(w, map[string]any{"status": "not_generated", "year": year})
		return
	}
	if err != nil {
		writeError(w, 500, "wrapped status unavailable")
		return
	}
	var resultValue, progressValue any
	_ = json.Unmarshal(result, &resultValue)
	_ = json.Unmarshal(progress, &progressValue)
	out := map[string]any{"status": string(status), "year": year}
	if string(status) == "done" {
		out["result"] = resultValue
	}
	if string(status) == "error" {
		out["error"] = string(errText)
	}
	if string(status) == "processing" {
		out["progress"] = progressValue
	}
	writeJSON(w, out)
}

func (h Handler) generate(w http.ResponseWriter, r *http.Request, uid string, year int) {
	var status string
	err := h.DB.QueryRowContext(r.Context(), `SELECT status FROM wrapped WHERE user_external_uid=? AND year=?`, uid, year).Scan(&status)
	if err == nil {
		if status == "done" {
			writeJSON(w, map[string]string{"status": "done", "message": "Your Wrapped 2025 is already generated"})
			return
		}
		if status == "processing" {
			writeJSON(w, map[string]string{"status": "processing", "message": "Generation is already in progress"})
			return
		}
	}
	progress, _ := json.Marshal(map[string]any{"phase": "queued", "updated_at": time.Now().UTC()})
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO wrapped(user_external_uid,year,status,result,error,progress,updated_at) VALUES(?,?, 'processing',JSON_OBJECT(),' ', ?, UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE status='processing',error='',progress=VALUES(progress),updated_at=UTC_TIMESTAMP(6)`, uid, year, progress)
	if err != nil {
		writeError(w, 500, "unable to start wrapped generation")
		return
	}
	go h.run(context.Background(), uid, year)
	writeJSON(w, map[string]string{"status": "processing", "message": "Starting Wrapped 2025 generation..."})
}

func (h Handler) run(ctx context.Context, uid string, year int) {
	var conversations int
	var seconds sql.NullFloat64
	err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(TIMESTAMPDIFF(SECOND,started_at,COALESCE(ended_at,started_at))),0) FROM conversations c JOIN users u ON u.id=c.user_id WHERE u.external_uid=? AND YEAR(c.started_at)=? AND c.status<>'failed'`, uid, year).Scan(&conversations, &seconds)
	if err != nil {
		h.fail(ctx, uid, year, err.Error())
		return
	}
	var memories int
	_ = h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories m JOIN users u ON u.id=m.user_id WHERE u.external_uid=? AND YEAR(m.created_at)=?`, uid, year).Scan(&memories)
	result, _ := json.Marshal(map[string]any{"year": year, "conversations": conversations, "conversation_seconds": seconds.Float64, "memories": memories, "generated_at": time.Now().UTC()})
	_, err = h.DB.ExecContext(ctx, `UPDATE wrapped SET status='done',result=?,error='',progress=JSON_OBJECT('phase','complete'),updated_at=UTC_TIMESTAMP(6) WHERE user_external_uid=? AND year=?`, result, uid, year)
	if err != nil {
		h.fail(ctx, uid, year, err.Error())
	}
}
func (h Handler) fail(ctx context.Context, uid string, year int, message string) {
	_, _ = h.DB.ExecContext(ctx, `UPDATE wrapped SET status='error',error=?,progress=JSON_OBJECT('phase','error'),updated_at=UTC_TIMESTAMP(6) WHERE user_external_uid=? AND year=?`, message, uid, year)
}
func routeYear(path string) (int, error) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, p := range parts {
		if p == "wrapped" && i+1 < len(parts) {
			return strconv.Atoi(parts[i+1])
		}
	}
	return 0, sql.ErrNoRows
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": detail})
}
