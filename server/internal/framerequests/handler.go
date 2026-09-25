package framerequests

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "golang.org/x/image/webp"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"remi/server/internal/auth"
	"remi/server/internal/signedurl"
)

type Handler struct {
	DB          *sql.DB
	StorageRoot string
	Store       Store
}
type createInput struct {
	DeviceID            string  `json:"device_id"`
	AccountGeneration   int64   `json:"account_generation"`
	DedupeKey           string  `json:"dedupe_key"`
	ConversationID      *string `json:"conversation_id"`
	ScreenshotID        *string `json:"screenshot_id"`
	RequestedTTLSeconds *int    `json:"requested_ttl_seconds"`
}
type stateInput struct {
	State             string  `json:"state"`
	DeviceID          string  `json:"device_id"`
	AccountGeneration int64   `json:"account_generation"`
	TerminalReason    *string `json:"terminal_reason"`
	StorageID         *string `json:"storage_id"`
	ByteCount         int64   `json:"byte_count"`
	ContentType       *string `json:"content_type"`
}
type promotionInput struct {
	DeviceID          string `json:"device_id"`
	AccountGeneration int64  `json:"account_generation"`
	ConversationID    string `json:"conversation_id"`
}

var terminal = map[string]bool{"attached": true, "offline": true, "pruned": true, "failed": true, "expired": true, "cancelled": true}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.Contains(path, "/shared/screenshots") && !strings.HasSuffix(path, "/image") {
		h.publicSharedScreenshots(w, r)
		return
	}
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if h.DB == nil {
		http.Error(w, "frame request storage is not configured", 503)
		return
	}
	switch {
	case strings.HasPrefix(path, "/v1/conversations/") && strings.HasSuffix(path, "/screenshots") && r.Method == http.MethodGet:
		h.listConversationScreenshots(w, r, uid)
	case strings.Contains(path, "/screenshots/") && strings.HasSuffix(path, "/image") && r.Method == http.MethodGet:
		h.photo(w, r, uid)
	case strings.Contains(path, "/shared/screenshots/") && strings.HasSuffix(path, "/image") && r.Method == http.MethodGet:
		h.publicPhoto(w, r)
	case strings.Contains(path, "/screenshots/") && r.Method == http.MethodDelete:
		h.deleteConversationScreenshot(w, r, uid)
	case strings.HasSuffix(path, "/screenshots") && r.Method == http.MethodDelete:
		h.deleteAllConversationScreenshots(w, r, uid)
	case strings.HasSuffix(path, "/screenshot-sharing") && r.Method == http.MethodPatch:
		h.updateScreenshotSharing(w, r, uid)
	case path == "/v1/frame-requests" && r.Method == http.MethodPost:
		h.create(w, r, uid)
	case path == "/v1/frame-requests/pending" && r.Method == http.MethodGet:
		h.pending(w, r, uid)
	case strings.HasPrefix(path, "/v1/frame-requests/status/") && r.Method == http.MethodGet:
		h.status(w, r, uid)
	case strings.HasPrefix(path, "/v1/frame-requests/temporary/") && strings.HasSuffix(path, "/image") && r.Method == http.MethodGet:
		h.temporaryImage(w, r, uid)
	case strings.HasSuffix(path, "/state") && r.Method == http.MethodPost:
		h.state(w, r, uid)
	case strings.HasSuffix(path, "/upload") && r.Method == http.MethodPost:
		h.upload(w, r, uid)
	case strings.HasSuffix(path, "/promote") && r.Method == http.MethodPost:
		h.promote(w, r, uid)
	case strings.Contains(path, "/conversations/") && strings.Contains(path, "/photos/") && strings.HasSuffix(path, "/image") && r.Method == http.MethodGet:
		h.photo(w, r, uid)
	default:
		http.NotFound(w, r)
	}
}

// temporaryImage releases pixels only for an uploaded, unattached request.
// Permanent conversation evidence remains behind the conversation-owned image
// handlers; this route is intentionally limited to the JIT temporary boundary.
func (h Handler) temporaryImage(w http.ResponseWriter, r *http.Request, uid string) {
	parts := strings.Split(strings.Trim(pathWithoutQuery(r.URL.Path), "/"), "/")
	if len(parts) != 5 || parts[0] != "v1" || parts[1] != "frame-requests" || parts[2] != "temporary" || parts[4] != "image" {
		http.NotFound(w, r)
		return
	}
	id := parts[3]
	accountGeneration := int64(queryInt(r, "account_generation", 0))
	f, err := h.get(r, id, uid)
	if err != nil || f.AccountGeneration != accountGeneration || f.ConversationID != nil {
		http.Error(w, "frame_request_not_found", http.StatusNotFound)
		return
	}
	if !f.ExpiresAt.After(time.Now().UTC()) {
		http.Error(w, "frame_request_expired", http.StatusGone)
		return
	}
	if f.State != "uploaded" || f.StorageID == nil || strings.TrimSpace(*f.StorageID) == "" {
		http.Error(w, "frame_request_"+f.State, http.StatusConflict)
		return
	}
	payload, err := h.read(uid, *f.StorageID)
	if err != nil {
		http.Error(w, "frame_request_pixels_unavailable", http.StatusNotFound)
		return
	}
	contentType := value(f.ContentType)
	if contentType == "" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func pathWithoutQuery(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}

func (h Handler) conversationIDFromScreenshotPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 4 && parts[0] == "v1" && parts[1] == "conversations" {
		return parts[2]
	}
	return ""
}

func (h Handler) loadConversationPhotos(ctx context.Context, conversationID, uid string) ([]map[string]any, bool, bool, error) {
	var raw []byte
	var sharing, enabled bool
	var visibility string
	query := `SELECT COALESCE(c.photos,JSON_ARRAY()),c.screenshot_sharing_enabled,c.visibility,u.meeting_note_screenshots_enabled FROM conversations c JOIN users u ON u.id=c.user_id WHERE c.id=? AND u.external_uid=?`
	if err := h.DB.QueryRowContext(ctx, query, conversationID, uid).Scan(&raw, &sharing, &visibility, &enabled); err != nil {
		return nil, false, false, err
	}
	photos := []map[string]any{}
	_ = json.Unmarshal(raw, &photos)
	return photos, sharing, enabled && visibility != "private", nil
}

func (h Handler) listConversationScreenshots(w http.ResponseWriter, r *http.Request, uid string) {
	id := h.conversationIDFromScreenshotPath(r.URL.Path)
	photos, _, visible, err := h.loadConversationPhotos(r.Context(), id, uid)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "failed to read conversation screenshots", 503)
		return
	}
	if !visible {
		photos = nil
	}
	writeJSON(w, screenFrameSetForUID(uid, id, photos, 0, false))
}

func (h Handler) deleteConversationScreenshot(w http.ResponseWriter, r *http.Request, uid string) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 {
		http.NotFound(w, r)
		return
	}
	id, frameID := parts[2], parts[4]
	photos, _, _, err := h.loadConversationPhotos(r.Context(), id, uid)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "failed to read conversation screenshots", 503)
		return
	}
	var kept []map[string]any
	var storageID string
	found := false
	for _, photo := range photos {
		if photo["id"] == frameID {
			found = true
			storageID, _ = photo["storage_id"].(string)
			continue
		}
		kept = append(kept, photo)
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	kept = normalizePhotoRoles(kept)
	raw, _ := json.Marshal(kept)
	if _, err = h.DB.ExecContext(r.Context(), `UPDATE conversations c JOIN users u ON u.id=c.user_id SET c.photos=? WHERE c.id=? AND u.external_uid=?`, raw, id, uid); err != nil {
		http.Error(w, "failed to delete conversation screenshot", 503)
		return
	}
	if storageID != "" {
		_ = h.remove(uid, storageID)
	}
	writeJSON(w, screenFrameSetForUID(uid, id, kept, 0, false))
}

func (h Handler) deleteAllConversationScreenshots(w http.ResponseWriter, r *http.Request, uid string) {
	id := h.conversationIDFromScreenshotPath(r.URL.Path)
	photos, _, _, err := h.loadConversationPhotos(r.Context(), id, uid)
	if err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "failed to read conversation screenshots", 503)
		return
	}
	if _, err = h.DB.ExecContext(r.Context(), `UPDATE conversations c JOIN users u ON u.id=c.user_id SET c.photos=JSON_ARRAY() WHERE c.id=? AND u.external_uid=?`, id, uid); err != nil {
		http.Error(w, "failed to delete conversation screenshots", 503)
		return
	}
	for _, photo := range photos {
		if storageID, ok := photo["storage_id"].(string); ok {
			_ = h.remove(uid, storageID)
		}
	}
	writeJSON(w, screenFrameSetForUID(uid, id, nil, 0, false))
}

func (h Handler) updateScreenshotSharing(w http.ResponseWriter, r *http.Request, uid string) {
	id := h.conversationIDFromScreenshotPath(r.URL.Path)
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || input.Enabled == nil {
		http.Error(w, "enabled is required", 400)
		return
	}
	if _, err := h.DB.ExecContext(r.Context(), `UPDATE conversations c JOIN users u ON u.id=c.user_id SET c.screenshot_sharing_enabled=? WHERE c.id=? AND u.external_uid=?`, *input.Enabled, id, uid); err != nil {
		http.Error(w, "failed to update screenshot sharing", 503)
		return
	}
	photos, sharing, visible, err := h.loadConversationPhotos(r.Context(), id, uid)
	if err != nil {
		http.Error(w, "failed to read conversation screenshots", 503)
		return
	}
	_ = sharing
	if !visible {
		photos = nil
	}
	writeJSON(w, screenFrameSetForUID(uid, id, photos, 0, false))
}

func (h Handler) publicSharedScreenshots(w http.ResponseWriter, r *http.Request) {
	id := h.conversationIDFromScreenshotPath(r.URL.Path)
	var raw []byte
	var sharing, enabled bool
	var visibility string
	err := h.DB.QueryRowContext(r.Context(), `SELECT COALESCE(c.photos,JSON_ARRAY()),c.screenshot_sharing_enabled,c.visibility,u.meeting_note_screenshots_enabled FROM conversations c JOIN users u ON u.id=c.user_id WHERE c.id=?`, id).Scan(&raw, &sharing, &visibility, &enabled)
	if err != nil || !sharing || !enabled || visibility == "private" {
		writeJSON(w, screenFrameSetForUID("", id, nil, 0, true))
		return
	}
	photos := []map[string]any{}
	_ = json.Unmarshal(raw, &photos)
	ownerUID := ""
	_ = h.DB.QueryRowContext(r.Context(), `SELECT u.external_uid FROM conversations c JOIN users u ON u.id=c.user_id WHERE c.id=?`, id).Scan(&ownerUID)
	writeJSON(w, screenFrameSetForUID(ownerUID, id, photos, 0, true))
}

// publicPhoto serves the pixels referenced by the public shared screenshot
// response. The URL is signed for the conversation owner, but the request is
// still re-authorized against the current sharing and account gates so a
// revoked share stops working immediately.
func (h Handler) publicPhoto(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(pathWithoutQuery(r.URL.Path), "/"), "/")
	if len(parts) != 7 || parts[0] != "v1" || parts[1] != "conversations" || parts[3] != "shared" || parts[4] != "screenshots" || parts[6] != "image" {
		http.NotFound(w, r)
		return
	}
	conversationID, photoID := parts[2], parts[5]
	var ownerUID string
	var raw []byte
	var sharing, enabled bool
	var visibility string
	err := h.DB.QueryRowContext(r.Context(), `SELECT u.external_uid,COALESCE(c.photos,JSON_ARRAY()),c.screenshot_sharing_enabled,c.visibility,u.meeting_note_screenshots_enabled FROM conversations c JOIN users u ON u.id=c.user_id WHERE c.id=?`, conversationID).Scan(&ownerUID, &raw, &sharing, &visibility, &enabled)
	if err != nil || !sharing || !enabled || visibility == "private" || !signedurl.Verify(pathWithoutQuery(r.URL.Path), ownerUID, r.URL.Query(), time.Now().UTC()) {
		http.NotFound(w, r)
		return
	}
	photos := []map[string]any{}
	_ = json.Unmarshal(raw, &photos)
	storageID := ""
	contentType := "image/jpeg"
	for _, photo := range photos {
		if photo["id"] == photoID {
			storageID, _ = photo["storage_id"].(string)
			if v, ok := photo["content_type"].(string); ok && v != "" {
				contentType = v
			}
			break
		}
	}
	if storageID == "" {
		http.NotFound(w, r)
		return
	}
	data, err := h.read(ownerUID, storageID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

func (h Handler) create(w http.ResponseWriter, r *http.Request, uid string) {
	var in createInput
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.DeviceID) == "" || strings.TrimSpace(in.DedupeKey) == "" || len(in.DeviceID) > 256 || len(in.DedupeKey) > 256 || in.AccountGeneration < 0 {
		http.Error(w, "invalid frame request", 400)
		return
	}
	ttl := 86400
	if in.RequestedTTLSeconds != nil {
		ttl = *in.RequestedTTLSeconds
	}
	if ttl < 1 || ttl > 6*24*60*60 {
		http.Error(w, "invalid requested_ttl_seconds", 400)
		return
	}
	now := time.Now().UTC()
	var existing frame
	err := h.DB.QueryRowContext(r.Context(), `SELECT request_id,user_external_uid,device_id,account_generation,dedupe_key,dedupe_window,attempt_number,conversation_id,screenshot_id,state,created_at,expires_at,claimed_at,uploaded_at,attached_at,terminal_reason,byte_count,content_type,storage_id,cleanup_state,cleanup_attempts,cleanup_next_attempt_at FROM frame_requests WHERE user_external_uid=? AND device_id=? AND account_generation=? AND dedupe_key=? AND dedupe_window=? AND expires_at>? ORDER BY created_at DESC LIMIT 1`, uid, in.DeviceID, in.AccountGeneration, in.DedupeKey, 0, now).Scan(existing.args()...)
	if err == nil {
		writeEnvelope(w, existing, true)
		return
	}
	id := "frame-" + uuid.NewString()
	expires := now.Add(time.Duration(ttl) * time.Second)
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO frame_requests(request_id,user_external_uid,device_id,account_generation,dedupe_key,dedupe_window,attempt_number,conversation_id,screenshot_id,state,created_at,expires_at,cleanup_state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, uid, in.DeviceID, in.AccountGeneration, in.DedupeKey, 0, 0, in.ConversationID, in.ScreenshotID, "requested", now, expires, "not_required")
	if err != nil {
		http.Error(w, "failed to enqueue frame request", 503)
		return
	}
	row := frame{RequestID: id, UID: uid, DeviceID: in.DeviceID, AccountGeneration: in.AccountGeneration, DedupeKey: in.DedupeKey, ConversationID: in.ConversationID, ScreenshotID: in.ScreenshotID, State: "requested", CreatedAt: now, ExpiresAt: expires, CleanupState: stringPtr("not_required")}
	writeEnvelope(w, row, false)
}

func (h Handler) status(w http.ResponseWriter, r *http.Request, uid string) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/frame-requests/status/")
	v, e := h.get(r, id, uid)
	if errors.Is(e, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, "failed to read frame request", 503)
		return
	}
	if v.AccountGeneration != int64(queryInt(r, "account_generation", 0)) {
		http.NotFound(w, r)
		return
	}
	writeEnvelope(w, v, false)
}
func (h Handler) pending(w http.ResponseWriter, r *http.Request, uid string) {
	device := r.URL.Query().Get("device_id")
	if device == "" {
		http.Error(w, "device_id is required", 400)
		return
	}
	limit := queryInt(r, "limit", 32)
	if limit < 1 || limit > 32 {
		http.Error(w, "invalid limit", 400)
		return
	}
	gen := queryInt(r, "account_generation", 0)
	rows, e := h.DB.QueryContext(r.Context(), `SELECT request_id,user_external_uid,device_id,account_generation,dedupe_key,dedupe_window,attempt_number,conversation_id,screenshot_id,state,created_at,expires_at,claimed_at,uploaded_at,attached_at,terminal_reason,byte_count,content_type,storage_id,cleanup_state,cleanup_attempts,cleanup_next_attempt_at FROM frame_requests WHERE user_external_uid=? AND device_id=? AND account_generation=? AND state IN ('requested','claimed') AND expires_at>? ORDER BY created_at ASC LIMIT ?`, uid, device, gen, time.Now().UTC(), limit)
	if e != nil {
		http.Error(w, "failed to list frame requests", 503)
		return
	}
	defer rows.Close()
	out := []frame{}
	for rows.Next() {
		v, e := scan(rows)
		if e != nil {
			http.Error(w, "failed to read frame request", 503)
			return
		}
		out = append(out, v)
	}
	writeJSON(w, map[string]any{"requests": out})
}
func (h Handler) state(w http.ResponseWriter, r *http.Request, uid string) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/frame-requests/"), "/state")
	var in stateInput
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.DeviceID) == "" || in.AccountGeneration < 0 {
		http.Error(w, "invalid state update", 400)
		return
	}
	if !validState(in.State) {
		http.Error(w, "invalid frame request state", 409)
		return
	}
	v, e := h.get(r, id, uid)
	if errors.Is(e, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, "failed to read frame request", 503)
		return
	}
	if v.DeviceID != in.DeviceID || v.AccountGeneration != in.AccountGeneration {
		http.Error(w, "frame_request_owner_mismatch", 403)
		return
	}
	if v.State == "attached" || terminal[v.State] {
		if v.State != in.State {
			http.Error(w, "frame request is terminal", 409)
			return
		}
		writeEnvelope(w, v, false)
		return
	}
	if terminal[in.State] && strings.TrimSpace(value(in.TerminalReason)) == "" {
		http.Error(w, "terminal frame requests require a reason", 409)
		return
	}
	if in.State == "uploaded" && strings.TrimSpace(value(in.StorageID)) == "" {
		http.Error(w, "uploaded frame requests require a storage id", 409)
		return
	}
	now := time.Now().UTC()
	_, e = h.DB.ExecContext(r.Context(), `UPDATE frame_requests SET state=?,terminal_reason=?,storage_id=?,byte_count=?,content_type=?,claimed_at=CASE WHEN ?='claimed' AND claimed_at IS NULL THEN ? ELSE claimed_at END,uploaded_at=CASE WHEN ?='uploaded' THEN ? ELSE uploaded_at END,attached_at=CASE WHEN ?='attached' THEN ? ELSE attached_at END,updated_at=? WHERE request_id=? AND user_external_uid=?`, in.State, in.TerminalReason, in.StorageID, in.ByteCount, in.ContentType, in.State, now, in.State, now, in.State, now, now, id, uid)
	if e != nil {
		http.Error(w, "failed to transition frame request", 503)
		return
	}
	v, e = h.get(r, id, uid)
	if e != nil {
		http.Error(w, "failed to read frame request", 503)
		return
	}
	writeEnvelope(w, v, false)
}
func (h Handler) upload(w http.ResponseWriter, r *http.Request, uid string) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/frame-requests/"), "/upload")
	device, generation := r.URL.Query().Get("device_id"), queryInt(r, "account_generation", 0)
	if device == "" || generation < 0 {
		http.Error(w, "device_id and account_generation are required", 400)
		return
	}
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		http.Error(w, "invalid multipart upload", 400)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required", 400)
		return
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, 10<<20+1))
	if err != nil {
		http.Error(w, "unable to read frame", 400)
		return
	}
	if len(payload) > 10<<20 {
		http.Error(w, "frame_upload_too_large", 413)
		return
	}
	canonical, contentType, err := canonicalImage(payload)
	if err != nil {
		http.Error(w, err.Error(), 415)
		return
	}
	v, err := h.get(r, id, uid)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "failed to read frame request", 503)
		return
	}
	if v.DeviceID != device || v.AccountGeneration != int64(generation) {
		http.Error(w, "frame_request_owner_mismatch", 403)
		return
	}
	if v.State != "requested" && v.State != "claimed" {
		http.Error(w, "frame request is not uploadable", 409)
		return
	}
	storageID := "temporary-" + uuid.NewString()
	if err := h.put(uid, storageID, canonical); err != nil {
		http.Error(w, "failed to persist frame pixels", 503)
		return
	}
	now := time.Now().UTC()
	_, err = h.DB.ExecContext(r.Context(), `UPDATE frame_requests SET state='uploaded',storage_id=?,byte_count=?,content_type=?,uploaded_at=?,updated_at=? WHERE request_id=? AND user_external_uid=? AND device_id=? AND account_generation=? AND state IN ('requested','claimed')`, storageID, len(canonical), contentType, now, now, id, uid, device, generation)
	if err != nil {
		_ = h.remove(uid, storageID)
		http.Error(w, "failed to commit frame upload", 503)
		return
	}
	v, err = h.get(r, id, uid)
	if err != nil {
		http.Error(w, "failed to read frame request", 503)
		return
	}
	writeEnvelope(w, v, false)
}

func (h Handler) promote(w http.ResponseWriter, r *http.Request, uid string) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/frame-requests/"), "/promote")
	var in promotionInput
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.DeviceID == "" || in.ConversationID == "" || in.AccountGeneration < 0 {
		http.Error(w, "invalid promotion", 400)
		return
	}
	v, e := h.get(r, id, uid)
	if errors.Is(e, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, "failed to read frame request", 503)
		return
	}
	if v.DeviceID != in.DeviceID || v.AccountGeneration != in.AccountGeneration || v.ConversationID == nil || *v.ConversationID != in.ConversationID {
		http.Error(w, "frame_request_owner_mismatch", 403)
		return
	}
	if v.State == "attached" {
		writeEnvelope(w, v, false)
		return
	}
	if v.State != "uploaded" || v.StorageID == nil {
		http.Error(w, "only uploaded frame requests may be promoted", 409)
		return
	}
	permanent := "permanent-" + strings.TrimPrefix(id, "frame-")
	if e = h.copy(uid, *v.StorageID, permanent); e != nil {
		http.Error(w, "failed to persist permanent frame", 503)
		return
	}
	now := time.Now().UTC()
	tx, e := h.DB.BeginTx(r.Context(), nil)
	if e != nil {
		_ = h.remove(uid, permanent)
		http.Error(w, "failed to start frame promotion", 503)
		return
	}
	var photosRaw []byte
	if e = tx.QueryRowContext(r.Context(), `SELECT c.photos FROM conversations c JOIN users u ON u.id=c.user_id WHERE c.id=? AND u.external_uid=? FOR UPDATE`, in.ConversationID, uid).Scan(&photosRaw); e != nil {
		_ = tx.Rollback()
		_ = h.remove(uid, permanent)
		http.Error(w, "conversation not found", http.StatusNotFound)
		return
	}
	photos := []map[string]any{}
	if len(photosRaw) > 0 {
		_ = json.Unmarshal(photosRaw, &photos)
	}
	photo := map[string]any{"id": id, "storage_id": permanent, "content_type": "image/jpeg", "created_at": now}
	seen := false
	for _, existing := range photos {
		if existing["id"] == id {
			seen = true
			break
		}
	}
	if !seen {
		photos = append(photos, photo)
	}
	encodedPhotos, e := json.Marshal(photos)
	if e != nil {
		_ = tx.Rollback()
		_ = h.remove(uid, permanent)
		http.Error(w, "failed to encode conversation photos", 503)
		return
	}
	if _, e = tx.ExecContext(r.Context(), `UPDATE conversations c JOIN users u ON u.id=c.user_id SET c.photos=? WHERE c.id=? AND u.external_uid=?`, encodedPhotos, in.ConversationID, uid); e != nil {
		_ = tx.Rollback()
		_ = h.remove(uid, permanent)
		http.Error(w, "failed to persist conversation photo", 503)
		return
	}
	_, e = tx.ExecContext(r.Context(), `UPDATE frame_requests SET state='attached',storage_id=?,attached_at=?,expires_at=created_at,cleanup_state='permanent',updated_at=? WHERE request_id=? AND user_external_uid=? AND device_id=? AND account_generation=? AND state='uploaded'`, permanent, now, now, id, uid, in.DeviceID, in.AccountGeneration)
	if e != nil {
		_ = tx.Rollback()
		_ = h.remove(uid, permanent)
		http.Error(w, "failed to commit frame promotion", 503)
		return
	}
	if e = tx.Commit(); e != nil {
		_ = h.remove(uid, permanent)
		http.Error(w, "failed to commit frame promotion", 503)
		return
	}
	_ = h.remove(uid, *v.StorageID)
	v, e = h.get(r, id, uid)
	if e != nil {
		http.Error(w, "failed to read promoted frame", 503)
		return
	}
	writeEnvelope(w, v, false)
}

func (h Handler) photo(w http.ResponseWriter, r *http.Request, uid string) {
	if r.URL.Query().Get("expires") != "" || r.URL.Query().Get("sig") != "" {
		if !signedurl.Verify(pathWithoutQuery(r.URL.Path), uid, r.URL.Query(), time.Now().UTC()) {
			http.Error(w, "invalid or expired screenshot URL", http.StatusForbidden)
			return
		}
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 6 {
		http.NotFound(w, r)
		return
	}
	conversationID, photoID := parts[2], parts[4]
	if len(parts) >= 6 && parts[3] == "screenshots" {
		photoID = parts[4]
	}
	var storageID, contentType string
	err := h.DB.QueryRowContext(r.Context(), `SELECT storage_id,COALESCE(content_type,'image/jpeg') FROM frame_requests WHERE request_id=? AND user_external_uid=? AND conversation_id=? AND state='attached'`, photoID, uid, conversationID).Scan(&storageID, &contentType)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := h.read(uid, storageID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

func (h Handler) root() string {
	if h.StorageRoot != "" {
		return h.StorageRoot
	}
	if v := os.Getenv("FRAME_REQUEST_STORAGE_ROOT"); v != "" {
		return v
	}
	return filepath.Join("data", "frame-requests")
}
func (h Handler) path(uid, storageID string) (string, error) {
	if uid == "" || storageID == "" || strings.ContainsAny(uid+storageID, `/\\`) || strings.HasPrefix(storageID, ".") {
		return "", fmt.Errorf("invalid frame storage path")
	}
	return filepath.Join(h.root(), uid, storageID+".jpg"), nil
}
func (h Handler) put(uid, storageID string, data []byte) error {
	return h.store().Put(context.Background(), uid, storageID, data)
}
func (h Handler) read(uid, storageID string) ([]byte, error) {
	return h.store().Get(context.Background(), uid, storageID)
}
func (h Handler) remove(uid, storageID string) error {
	return h.store().Delete(context.Background(), uid, storageID)
}
func (h Handler) copy(uid, from, to string) error {
	return h.store().Copy(context.Background(), uid, from, to)
}

func (h Handler) store() Store {
	if h.Store != nil {
		return h.Store
	}
	return LocalStore{Root: h.root()}
}
func canonicalImage(payload []byte) ([]byte, string, error) {
	cfg, format, e := image.DecodeConfig(bytes.NewReader(payload))
	_ = format
	if e != nil {
		return nil, "", fmt.Errorf("frame_upload_invalid_image")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width*cfg.Height > 25_000_000 {
		return nil, "", fmt.Errorf("frame_upload_dimensions_too_large")
	}
	img, format, e := image.Decode(bytes.NewReader(payload))
	if e != nil {
		return nil, "", fmt.Errorf("frame_upload_invalid_image")
	}
	_ = format
	var out []byte
	buf := new(bytes.Buffer)
	if e = jpeg.Encode(buf, img, &jpeg.Options{Quality: 85}); e != nil {
		return nil, "", e
	}
	out = buf.Bytes()
	return out, "image/jpeg", nil
}

type frame struct {
	RequestID, UID, DeviceID, DedupeKey, State                                         string
	AccountGeneration                                                                  int64
	DedupeWindow, AttemptNumber                                                        int
	ConversationID, ScreenshotID, TerminalReason, ContentType, StorageID, CleanupState *string
	CreatedAt, ExpiresAt, ClaimedAt, UploadedAt, AttachedAt, CleanupNextAttemptAt      time.Time
	ByteCount                                                                          int64
	CleanupAttempts                                                                    int
}

func (f *frame) args() []any {
	return []any{&f.RequestID, &f.UID, &f.DeviceID, &f.AccountGeneration, &f.DedupeKey, &f.DedupeWindow, &f.AttemptNumber, &f.ConversationID, &f.ScreenshotID, &f.State, &f.CreatedAt, &f.ExpiresAt, &f.ClaimedAt, &f.UploadedAt, &f.AttachedAt, &f.TerminalReason, &f.ByteCount, &f.ContentType, &f.StorageID, &f.CleanupState, &f.CleanupAttempts, &f.CleanupNextAttemptAt}
}
func scan(s interface{ Scan(...any) error }) (frame, error) {
	var f frame
	err := s.Scan(f.args()...)
	return f, err
}
func (h Handler) get(r *http.Request, id, uid string) (frame, error) {
	return scan(h.DB.QueryRowContext(r.Context(), `SELECT request_id,user_external_uid,device_id,account_generation,dedupe_key,dedupe_window,attempt_number,conversation_id,screenshot_id,state,created_at,expires_at,claimed_at,uploaded_at,attached_at,terminal_reason,byte_count,content_type,storage_id,cleanup_state,cleanup_attempts,cleanup_next_attempt_at FROM frame_requests WHERE request_id=? AND user_external_uid=?`, id, uid))
}
func validState(s string) bool {
	switch s {
	case "requested", "claimed", "uploaded", "attached", "offline", "pruned", "failed", "expired", "cancelled":
		return true
	}
	return false
}
func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func stringPtr(v string) *string { return &v }
func queryInt(r *http.Request, k string, d int) int {
	v, e := strconv.Atoi(r.URL.Query().Get(k))
	if e != nil {
		return d
	}
	return v
}
func writeEnvelope(w http.ResponseWriter, f frame, dedup bool) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"request": f.toMap(), "deduplicated": dedup})
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func screenFrameSet(conversationID string, photos []map[string]any, revision int) map[string]any {
	return screenFrameSetForUID("", conversationID, photos, revision, false)
}

func screenFrameSetForUID(uid, conversationID string, photos []map[string]any, revision int, shared bool) map[string]any {
	frames := make([]map[string]any, 0, len(photos))
	var banner map[string]any
	for _, photo := range photos {
		id, _ := photo["id"].(string)
		if strings.TrimSpace(id) == "" {
			continue
		}
		path := "/v1/conversations/" + conversationID + "/screenshots/" + id + "/image"
		if shared {
			path = "/v1/conversations/" + conversationID + "/shared/screenshots/" + id + "/image"
		}
		contentURL := path
		if uid != "" {
			if signed, err := signedurl.Build(path, uid, time.Now().UTC().Add(time.Hour)); err == nil {
				contentURL = signed
			}
		}
		frame := map[string]any{
			"id":            id,
			"role":          "strip",
			"rank":          len(frames),
			"caption":       "",
			"labels":        []string{},
			"content_url":   contentURL,
			"thumbnail_url": contentURL,
		}
		if captured, ok := photo["captured_at"]; ok {
			frame["captured_at"] = captured
		}
		if role, _ := photo["role"].(string); role == "banner" {
			banner = frame
			banner["role"] = "banner"
			banner["rank"] = 0
		} else {
			frames = append(frames, frame)
		}
	}
	return map[string]any{"revision": revision, "banner": banner, "strip": frames}
}

func normalizePhotoRoles(photos []map[string]any) []map[string]any {
	sort.SliceStable(photos, func(i, j int) bool { return photoCapturedAt(photos[i]).Before(photoCapturedAt(photos[j])) })
	banner := -1
	best := 0.35
	for i, photo := range photos {
		if suitability, ok := photo["banner_suitability"].(float64); ok && suitability >= best {
			banner, best = i, suitability
		}
	}
	stripRank := 0
	for i, photo := range photos {
		if i == banner {
			photo["role"], photo["rank"] = "banner", 0
		} else {
			photo["role"], photo["rank"] = "strip", stripRank
			stripRank++
		}
	}
	return photos
}

func photoCapturedAt(photo map[string]any) time.Time {
	value, _ := photo["captured_at"].(string)
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Unix(0, 0).UTC()
	}
	return parsed
}
func (f frame) toMap() map[string]any {
	return map[string]any{"request_id": f.RequestID, "uid": f.UID, "device_id": f.DeviceID, "account_generation": f.AccountGeneration, "dedupe_key": f.DedupeKey, "dedupe_window": f.DedupeWindow, "attempt_number": f.AttemptNumber, "conversation_id": f.ConversationID, "screenshot_id": f.ScreenshotID, "state": f.State, "created_at": f.CreatedAt, "expires_at": f.ExpiresAt, "claimed_at": f.ClaimedAt, "uploaded_at": f.UploadedAt, "attached_at": f.AttachedAt, "terminal_reason": f.TerminalReason, "byte_count": f.ByteCount, "content_type": f.ContentType, "storage_id": f.StorageID, "cleanup_state": f.CleanupState, "cleanup_attempts": f.CleanupAttempts, "cleanup_next_attempt_at": f.CleanupNextAttemptAt}
}
