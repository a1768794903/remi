package screenframes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"remi/server/internal/auth"
	"remi/server/internal/framerequests"
	"remi/server/internal/signedurl"
)

type Handler struct {
	Judge Judge
	DB    *sql.DB
	Store framerequests.Store
}

type Judge interface {
	Judge(context.Context, string, []byte) (Judgement, error)
}
type Judgement struct {
	Outcome           string   `json:"outcome"`
	RejectReason      *string  `json:"reject_reason"`
	Caption           string   `json:"caption"`
	Labels            []string `json:"labels"`
	SourceBadge       *string  `json:"source_badge"`
	BannerSuitability float64  `json:"banner_suitability"`
}

type HTTPJudge struct {
	Endpoint string
	Client   *http.Client
}

func (j HTTPJudge) Judge(ctx context.Context, uid string, jpegBytes []byte) (Judgement, error) {
	payload, _ := json.Marshal(map[string]any{"uid": uid, "purpose": "meeting_note_v1", "image_base64": base64.StdEncoding.EncodeToString(jpegBytes), "mime_type": "image/jpeg"})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, j.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return Judgement{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := j.Client
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return Judgement{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Judgement{}, fmt.Errorf("judge returned %s", response.Status)
	}
	var result Judgement
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return Judgement{}, err
	}
	if err := validateJudgement(result); err != nil {
		return Judgement{}, err
	}
	return result, nil
}

type Request struct {
	SchemaVersion int         `json:"schema_version"`
	AttemptID     uuid.UUID   `json:"attempt_id"`
	Purpose       string      `json:"purpose"`
	Subject       Subject     `json:"subject"`
	Candidates    []Candidate `json:"candidates"`
}
type Subject struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type Candidate struct {
	ClientFrameID  string `json:"client_frame_id"`
	CapturedAt     string `json:"captured_at"`
	MimeType       string `json:"mime_type"`
	DeclaredWidth  int    `json:"declared_width"`
	DeclaredHeight int    `json:"declared_height"`
	SHA256Base64   string `json:"sha256_base64"`
	BytesBase64    string `json:"bytes_base64"`
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in Request
	decoder := json.NewDecoder(io.LimitReader(r.Body, 32<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		writeCode(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := Validate(in); err != nil {
		writeCode(w, http.StatusBadRequest, err.Error())
		return
	}
	if os.Getenv("SCREEN_FRAME_EGRESS_ENABLED") != "true" {
		writeCode(w, http.StatusConflict, "screen_frame_egress_unavailable")
		return
	}
	if h.DB != nil {
		fingerprintBytes, _ := json.Marshal(in)
		sum := sha256.Sum256(fingerprintBytes)
		response, state, reserveErr := h.reserveAttempt(r.Context(), uid, in.Purpose, in.AttemptID, hex.EncodeToString(sum[:]))
		if reserveErr != nil {
			writeCode(w, http.StatusServiceUnavailable, "attempt_storage_unavailable")
			return
		}
		if state == "conflict" {
			writeCode(w, http.StatusConflict, "attempt_id_reused_with_different_request")
			return
		}
		if state == "replay" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(response)
			return
		}
		if state == "in_progress" {
			writeCode(w, http.StatusServiceUnavailable, "adjudication_in_progress_retry")
			return
		}
	}
	judge := h.Judge
	if judge == nil && strings.TrimSpace(os.Getenv("SCREEN_FRAME_JUDGE_ENDPOINT")) != "" {
		judge = HTTPJudge{Endpoint: strings.TrimSpace(os.Getenv("SCREEN_FRAME_JUDGE_ENDPOINT"))}
	}
	if judge == nil {
		writeCode(w, http.StatusServiceUnavailable, "judge_unavailable")
		return
	}
	approved := make([]approvedCandidate, 0, len(in.Candidates))
	for _, candidate := range in.Candidates {
		raw, decodeErr := decodeAndVerify(candidate.BytesBase64, candidate.SHA256Base64)
		if decodeErr != nil {
			continue
		}
		canonical, err := canonicalJPEG(raw)
		if err != nil {
			continue
		}
		judgement, err := judge.Judge(r.Context(), uid, canonical)
		if err != nil {
			continue
		}
		if judgement.Outcome == "approved_clean" {
			approved = append(approved, approvedCandidate{Candidate: candidate, JPEG: canonical, Judgement: judgement})
		}
	}
	if len(approved) == 0 {
		result := map[string]any{"attempt_id": in.AttemptID, "outcome": "no_approved_frames", "frame_set": emptyFrameSet()}
		h.storeAttemptResult(r.Context(), uid, in.AttemptID, result)
		writeJSON(w, http.StatusOK, result)
		return
	}
	if h.DB == nil || h.Store == nil {
		writeCode(w, http.StatusServiceUnavailable, "screen_frame_writer_unavailable")
		return
	}
	frameSet, err := h.persistApproved(r.Context(), uid, in, approved)
	if err != nil {
		writeCode(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	result := map[string]any{"attempt_id": in.AttemptID, "outcome": "committed", "frame_set": frameSet}
	h.storeAttemptResult(r.Context(), uid, in.AttemptID, result)
	writeJSON(w, http.StatusOK, result)
}

type approvedCandidate struct {
	Candidate Candidate
	JPEG      []byte
	Judgement Judgement
}

func (h Handler) reserveAttempt(ctx context.Context, uid, purpose string, id uuid.UUID, fingerprint string) ([]byte, string, error) {
	var existingFingerprint string
	var response []byte
	err := h.DB.QueryRowContext(ctx, `SELECT fingerprint,response FROM screen_frame_adjudication_attempts WHERE user_external_uid=? AND purpose=? AND attempt_id=?`, uid, purpose, id.String()).Scan(&existingFingerprint, &response)
	if err == nil {
		if existingFingerprint != fingerprint {
			return nil, "conflict", nil
		}
		if len(response) > 0 {
			return response, "replay", nil
		}
		return nil, "in_progress", nil
	}
	if err != sql.ErrNoRows {
		return nil, "", err
	}
	if _, err = h.DB.ExecContext(ctx, `INSERT INTO screen_frame_adjudication_attempts(user_external_uid,purpose,attempt_id,fingerprint,response,created_at) VALUES(?,?,?, ?,NULL,UTC_TIMESTAMP(6))`, uid, purpose, id.String(), fingerprint); err == nil {
		return nil, "new", nil
	}
	return h.reserveAttemptAfterRace(ctx, uid, purpose, id, fingerprint)
}

func (h Handler) reserveAttemptAfterRace(ctx context.Context, uid, purpose string, id uuid.UUID, fingerprint string) ([]byte, string, error) {
	var existingFingerprint string
	var response []byte
	if err := h.DB.QueryRowContext(ctx, `SELECT fingerprint,response FROM screen_frame_adjudication_attempts WHERE user_external_uid=? AND purpose=? AND attempt_id=?`, uid, purpose, id.String()).Scan(&existingFingerprint, &response); err != nil {
		return nil, "", err
	}
	if existingFingerprint != fingerprint {
		return nil, "conflict", nil
	}
	if len(response) > 0 {
		return response, "replay", nil
	}
	return nil, "in_progress", nil
}

func (h Handler) storeAttemptResult(ctx context.Context, uid string, id uuid.UUID, result map[string]any) {
	if h.DB == nil {
		return
	}
	encoded, err := json.Marshal(result)
	if err == nil {
		_, _ = h.DB.ExecContext(ctx, `UPDATE screen_frame_adjudication_attempts SET response=? WHERE user_external_uid=? AND attempt_id=?`, encoded, uid, id.String())
	}
}

func (h Handler) persistApproved(ctx context.Context, uid string, in Request, approved []approvedCandidate) (map[string]any, error) {
	var status string
	var started, ended time.Time
	var enabled bool
	if err := h.DB.QueryRowContext(ctx, `SELECT c.status,c.started_at,c.ended_at,u.meeting_note_screenshots_enabled FROM conversations c JOIN users u ON u.id=c.user_id WHERE c.id=? AND u.external_uid=?`, in.Subject.ID, uid).Scan(&status, &started, &ended, &enabled); err != nil {
		return nil, fmt.Errorf("conversation_not_found")
	}
	if status != "completed" {
		return nil, fmt.Errorf("conversation_not_completed")
	}
	if !enabled {
		return nil, fmt.Errorf("meeting_note_screenshots_disabled")
	}
	for _, item := range approved {
		captured, err := time.Parse(time.RFC3339Nano, item.Candidate.CapturedAt)
		if err != nil || captured.Before(started.Add(-120*time.Second)) || captured.After(ended.Add(120*time.Second)) {
			return nil, fmt.Errorf("captured_at_outside_conversation_window")
		}
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(photos,JSON_ARRAY()) FROM conversations c JOIN users u ON u.id=c.user_id WHERE c.id=? AND u.external_uid=? FOR UPDATE`, in.Subject.ID, uid).Scan(&raw); err != nil {
		return nil, err
	}
	photos := []map[string]any{}
	_ = json.Unmarshal(raw, &photos)
	storedIDs := []string{}
	cleanup := func() {
		for _, id := range storedIDs {
			_ = h.Store.Delete(ctx, uid, id)
		}
	}
	for _, item := range approved {
		if len(photos) >= 7 {
			break
		}
		id := "screen-" + uuid.NewString()
		approval, approvalErr := mintApproval(uid, in.Purpose, in.Subject.ID, item.JPEG, time.Now().UTC())
		if approvalErr != nil {
			return nil, approvalErr
		}
		if err = verifyApproval(approval, uid, in.Purpose, in.Subject.ID, item.JPEG, time.Now().UTC()); err != nil {
			return nil, err
		}
		if err = h.Store.Put(ctx, uid, id, item.JPEG); err != nil {
			cleanup()
			return nil, err
		}
		storedIDs = append(storedIDs, id)
		photos = append(photos, map[string]any{"id": id, "storage_id": id, "content_type": "image/jpeg", "created_at": time.Now().UTC(), "captured_at": item.Candidate.CapturedAt, "caption": item.Judgement.Caption, "labels": item.Judgement.Labels, "source_badge": item.Judgement.SourceBadge, "width": item.Candidate.DeclaredWidth, "height": item.Candidate.DeclaredHeight, "ground": ComputeGround(item.JPEG)})
	}
	encoded, _ := json.Marshal(photos)
	if _, err = tx.ExecContext(ctx, `UPDATE conversations c JOIN users u ON u.id=c.user_id SET c.photos=? WHERE c.id=? AND u.external_uid=?`, encoded, in.Subject.ID, uid); err != nil {
		cleanup()
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		cleanup()
		return nil, err
	}
	return frameSetFromPhotos(uid, in.Subject.ID, photos), nil
}

func Validate(in Request) error {
	if in.SchemaVersion != 1 {
		return fmt.Errorf("schema_version_invalid")
	}
	if in.AttemptID == uuid.Nil {
		return fmt.Errorf("attempt_id_invalid")
	}
	if in.Purpose != "meeting_note_v1" {
		return fmt.Errorf("unknown_purpose")
	}
	if in.Subject.Kind != "conversation" || strings.TrimSpace(in.Subject.ID) == "" || len(in.Subject.ID) > 256 {
		return fmt.Errorf("unsupported_subject")
	}
	if len(in.Candidates) < 1 || len(in.Candidates) > 8 {
		return fmt.Errorf("candidate_count_invalid")
	}
	for _, candidate := range in.Candidates {
		if strings.TrimSpace(candidate.ClientFrameID) == "" || len(candidate.ClientFrameID) > 128 {
			return fmt.Errorf("client_frame_id_invalid")
		}
		if candidate.MimeType != "image/jpeg" && candidate.MimeType != "image/png" {
			return fmt.Errorf("mime_type_invalid")
		}
		if candidate.DeclaredWidth < 1 || candidate.DeclaredWidth > 10000 || candidate.DeclaredHeight < 1 || candidate.DeclaredHeight > 10000 {
			return fmt.Errorf("declared_dimensions_invalid")
		}
		if _, err := decodeAndVerify(candidate.BytesBase64, candidate.SHA256Base64); err != nil {
			return err
		}
	}
	return nil
}

func decodeAndVerify(payload, digest string) ([]byte, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("undecodable_bytes_base64")
	}
	declared, err := base64.StdEncoding.Strict().DecodeString(digest)
	if err != nil || len(declared) != sha256.Size {
		return nil, fmt.Errorf("undecodable_sha256_base64")
	}
	actual := sha256.Sum256(raw)
	if string(actual[:]) != string(declared) {
		return nil, fmt.Errorf("digest_mismatch")
	}
	return raw, nil
}

func canonicalJPEG(raw []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width*cfg.Height > 25_000_000 {
		return nil, fmt.Errorf("canonicalization_failed")
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("canonicalization_failed")
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, decoded, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func validateJudgement(j Judgement) error {
	if j.Outcome != "approved_clean" && j.Outcome != "rejected" {
		return fmt.Errorf("invalid_judge_outcome")
	}
	if j.Outcome == "approved_clean" && j.RejectReason != nil {
		return fmt.Errorf("contradictory_judge_output")
	}
	if j.Outcome == "rejected" && j.RejectReason == nil {
		return fmt.Errorf("contradictory_judge_output")
	}
	if len(j.Caption) > 160 || len(j.Labels) > 8 || j.BannerSuitability < 0 || j.BannerSuitability > 1 {
		return fmt.Errorf("invalid_judge_metadata")
	}
	return nil
}

func emptyFrameSet() map[string]any {
	return map[string]any{"revision": 0, "banner": nil, "strip": []any{}}
}

func frameSetFromPhotos(uid, conversationID string, photos []map[string]any) map[string]any {
	strip := make([]map[string]any, 0, len(photos))
	for index, photo := range photos {
		id, _ := photo["id"].(string)
		if id == "" {
			continue
		}
		path := "/v1/conversations/" + conversationID + "/screenshots/" + id + "/image"
		contentURL, err := signedurl.Build(path, uid, time.Now().UTC().Add(time.Hour))
		if err != nil {
			contentURL = path
		}
		strip = append(strip, map[string]any{"id": id, "role": "strip", "rank": index, "caption": photo["caption"], "labels": photo["labels"], "source_badge": photo["source_badge"], "width": photo["width"], "height": photo["height"], "ground": photo["ground"], "content_url": contentURL, "thumbnail_url": contentURL, "url_expires_at": time.Now().UTC().Add(time.Hour)})
	}
	return map[string]any{"revision": 1, "banner": nil, "strip": strip}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeCode(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
}
