package notificationapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

const hourlyLimit = 10

type Handler struct{ DB *sql.DB }

func (h Handler) apiKey(w http.ResponseWriter, r *http.Request, appID string) (string, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		http.Error(w, "Missing or invalid Authorization header", 401)
		return "", false
	}
	sum := sha256.Sum256([]byte(parts[1]))
	var storedApp string
	if err := h.DB.QueryRowContext(r.Context(), `SELECT app_id FROM app_api_keys WHERE key_hash=?`, hex.EncodeToString(sum[:])).Scan(&storedApp); err != nil || storedApp != appID {
		http.Error(w, "Invalid API key", 403)
		return "", false
	}
	return storedApp, true
}

func (h Handler) Integration(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		http.Error(w, "notification storage is not configured", 503)
		return
	}
	var in struct {
		AppID   string `json:"aid"`
		UID     string `json:"uid"`
		Message string `json:"message"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil || strings.TrimSpace(in.AppID) == "" || strings.TrimSpace(in.UID) == "" || strings.TrimSpace(in.Message) == "" {
		http.Error(w, "aid, uid and message are required", 400)
		return
	}
	if _, ok := h.apiKey(w, r, in.AppID); !ok {
		return
	}
	var enabled int
	var ext []byte
	if err := h.DB.QueryRowContext(r.Context(), `SELECT 1 FROM user_enabled_apps WHERE user_external_uid=? AND app_id=?`, in.UID, in.AppID).Scan(&enabled); err != nil {
		http.Error(w, "User does not have this app installed", 403)
		return
	}
	if err := h.DB.QueryRowContext(r.Context(), `SELECT external_integration FROM plugins_data WHERE id=? AND approved=1 AND disabled=0`, in.AppID).Scan(&ext); err != nil {
		http.Error(w, "App not found", 404)
		return
	}
	var integration map[string]any
	_ = json.Unmarshal(ext, &integration)
	if v, ok := integration["chat_messages_enabled"].(bool); ok && !v {
		http.Error(w, "App notification capability is disabled", 403)
		return
	}
	var count int
	if err := h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM integration_notification_events WHERE app_id=? AND user_external_uid=? AND created_at>=UTC_TIMESTAMP()-INTERVAL 1 HOUR`, in.AppID, in.UID).Scan(&count); err != nil {
		http.Error(w, "notification storage unavailable", 503)
		return
	}
	if count >= hourlyLimit {
		w.Header().Set("Retry-After", "3600")
		http.Error(w, "Rate limit exceeded. Maximum 10 notifications per hour.", 429)
		return
	}
	if _, err := h.DB.ExecContext(r.Context(), `INSERT INTO integration_notification_events(app_id,user_external_uid,message,source,created_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6))`, in.AppID, in.UID, in.Message, "api.v1.integration"); err != nil {
		http.Error(w, "notification dispatch unavailable", 503)
		return
	}
	writeJSON(w, map[string]string{"status": "Ok"})
}

func (h Handler) Admin(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		http.Error(w, "notification storage is not configured", 503)
		return
	}
	if os.Getenv("ADMIN_KEY") == "" || r.Header.Get("secret-key") != os.Getenv("ADMIN_KEY") {
		http.Error(w, "You are not authorized to perform this action", 403)
		return
	}
	var in struct {
		UID   string         `json:"uid"`
		Title string         `json:"title"`
		Body  string         `json:"body"`
		Data  map[string]any `json:"data"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil || strings.TrimSpace(in.UID) == "" || strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Body) == "" {
		http.Error(w, "uid, title and body are required", 400)
		return
	}
	data, _ := json.Marshal(in.Data)
	if _, e := h.DB.ExecContext(r.Context(), `INSERT INTO integration_notification_events(app_id,user_external_uid,message,source,metadata,created_at) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6))`, "__admin__", in.UID, in.Title+"\n"+in.Body, "admin", data); e != nil {
		http.Error(w, "notification dispatch unavailable", 503)
		return
	}
	writeJSON(w, map[string]string{"status": "Ok"})
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
