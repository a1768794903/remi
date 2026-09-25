package mobilefeedback

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"remi/server/internal/auth"
)

type Handler struct{ DB *sql.DB }

type request struct {
	Schema      string  `json:"schema_version"`
	FeedbackID  string  `json:"feedback_id"`
	Kind        string  `json:"kind"`
	TargetKind  *string `json:"target_kind"`
	TargetID    string  `json:"target_id"`
	Value       int     `json:"value"`
	Reason      *string `json:"reason"`
	Comment     *string `json:"comment"`
	AppVersion  *string `json:"app_version"`
	AppBuild    *string `json:"app_build"`
	Platform    *string `json:"platform"`
	Namespace   *string `json:"client_app_namespace"`
	Profile     *string `json:"client_app_profile"`
	Correlation *string `json:"correlation_id"`
}

func (h Handler) Submit(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.DB == nil {
		http.Error(w, "feedback storage is not configured", http.StatusServiceUnavailable)
		return
	}
	var in request
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid feedback", http.StatusUnprocessableEntity)
		return
	}
	in.FeedbackID, in.TargetID = strings.TrimSpace(in.FeedbackID), strings.TrimSpace(in.TargetID)
	if in.FeedbackID == "" || in.TargetID == "" || len(in.FeedbackID) > 128 || len(in.TargetID) > 256 || (in.Value != 1 && in.Value != -1) {
		http.Error(w, "invalid feedback", http.StatusUnprocessableEntity)
		return
	}
	if in.Schema != "" && in.Schema != "mobile_feedback.v1" {
		http.Error(w, "unsupported schema_version", http.StatusUnprocessableEntity)
		return
	}
	if in.Comment != nil && len([]rune(*in.Comment)) > 1000 {
		http.Error(w, "feedback field is too long", http.StatusUnprocessableEntity)
		return
	}
	if in.Kind != "summary_helpfulness" && in.Kind != "recording_quality" {
		http.Error(w, "invalid feedback kind", http.StatusUnprocessableEntity)
		return
	}
	targetKind := "conversation"
	if in.TargetKind != nil {
		targetKind = strings.TrimSpace(*in.TargetKind)
	}
	if in.Kind == "summary_helpfulness" && targetKind != "conversation" {
		http.Error(w, "summary feedback must target a conversation", http.StatusUnprocessableEntity)
		return
	}
	if targetKind != "conversation" && targetKind != "recording" {
		http.Error(w, "invalid target_kind", http.StatusUnprocessableEntity)
		return
	}
	if in.Reason != nil && !validReason(in.Kind, strings.TrimSpace(*in.Reason)) {
		http.Error(w, "reason does not belong to feedback kind", http.StatusUnprocessableEntity)
		return
	}
	var relatedConversation string
	if targetKind == "conversation" {
		err = h.DB.QueryRowContext(r.Context(), `SELECT CAST(c.id AS CHAR) FROM conversations c JOIN users u ON u.id=c.user_id WHERE CAST(c.id AS CHAR)=? AND u.external_uid=?`, in.TargetID, uid).Scan(&relatedConversation)
	} else {
		err = h.DB.QueryRowContext(r.Context(), `SELECT conversation_id FROM recording_sessions WHERE recording_session_id=? AND user_external_uid=?`, in.TargetID, uid).Scan(&relatedConversation)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	payload, _ := json.Marshal(in)
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	eventID := "mobile-feedback-" + hash[:32]
	var oldHash, oldEvent string
	err = h.DB.QueryRowContext(r.Context(), `SELECT payload_hash,event_id FROM mobile_feedback WHERE user_external_uid=? AND feedback_id=?`, uid, in.FeedbackID).Scan(&oldHash, &oldEvent)
	if err == nil {
		if oldHash != hash {
			http.Error(w, "feedback_id was already used for a different event", http.StatusConflict)
			return
		}
		writeReceipt(w, in.FeedbackID, oldEvent, false)
		return
	}
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO mobile_feedback(user_external_uid,feedback_id,event_id,payload_hash,kind,target_kind,target_id,related_conversation_id,value,reason,comment,platform,app_version,app_build,client_app_namespace,client_app_profile,correlation_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, uid, in.FeedbackID, eventID, hash, in.Kind, targetKind, in.TargetID, nullableString(relatedConversation), in.Value, in.Reason, in.Comment, in.Platform, in.AppVersion, in.AppBuild, in.Namespace, in.Profile, in.Correlation, time.Now().UTC())
	if err != nil {
		http.Error(w, "Feedback could not be durably stored; retry safely", http.StatusServiceUnavailable)
		return
	}
	writeReceipt(w, in.FeedbackID, eventID, true)
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func validReason(kind, reason string) bool {
	values := []string{"summary_inaccurate", "summary_incomplete", "summary_irrelevant", "summary_wrong_context", "summary_other"}
	if kind != "summary_helpfulness" {
		values = []string{"recording_missing_audio", "recording_poor_transcription", "recording_wrong_speaker", "recording_delayed_or_stuck", "recording_fragmented_or_duplicated", "recording_other"}
	}
	for _, value := range values {
		if reason == value {
			return true
		}
	}
	return false
}
func writeReceipt(w http.ResponseWriter, feedbackID, eventID string, created bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": "mobile_feedback_receipt.v1", "feedback_id": feedbackID, "event_id": eventID, "created": created, "persisted": true})
}
