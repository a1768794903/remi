package account

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"remi/server/ent"
)

// wipeSQLTables contains user-owned tables that are intentionally outside the
// Ent graph. Keep this list explicit: a deletion must never be broadened to a
// table without an external UID predicate.
type wipeSQLTable struct {
	name  string
	owner string
}

var wipeSQLTables = []wipeSQLTable{
	{"user_byok", "user_external_uid"}, {"staged_tasks", "user_external_uid"},
	{"workstreams", "user_external_uid"}, {"workstream_events", "user_external_uid"},
	{"workstream_artifacts", "user_external_uid"}, {"workstream_checkpoints", "user_external_uid"},
	{"work_intent_receipts", "user_external_uid"}, {"task_goal_link_receipts", "user_external_uid"},
	{"candidates", "user_external_uid"}, {"speech_profiles", "user_external_uid"},
	{"referral_claims", "referred_uid"}, {"referral_claims", "referrer_uid"},
	{"mobile_feedback", "user_external_uid"}, {"advice", "user_external_uid"},
	{"recording_sessions", "user_external_uid"}, {"frame_requests", "user_external_uid"},
	{"plugins_data", "uid"}, {"user_enabled_apps", "user_external_uid"},
	{"app_reviews", "reviewer_uid"}, {"app_testers", "uid"},
	{"mcp_api_keys", "user_external_uid"}, {"mcp_oauth_grants", "user_external_uid"},
	{"dev_api_keys", "user_external_uid"}, {"mcp_oauth_authorization_codes", "user_external_uid"},
	{"mcp_oauth_access_tokens", "user_external_uid"}, {"mcp_oauth_refresh_tokens", "user_external_uid"},
	{"realtime_sessions", "user_external_uid"}, {"realtime_usage", "user_external_uid"},
	{"llm_usage", "user_external_uid"}, {"daily_summaries", "user_external_uid"},
	{"focus_sessions", "user_external_uid"}, {"screen_activity", "user_external_uid"},
	{"people", "user_external_uid"}, {"desktop_daily_usage", "user_external_uid"},
	{"memory_mutation_journal", "user_external_uid"}, {"memory_review_conflicts", "user_external_uid"},
	{"memory_import_runs", "user_external_uid"}, {"memory_import_artifacts", "user_external_uid"},
	{"feedback_events", "user_external_uid"},
	{"x_posts", "user_external_uid"}, {"conversation_ingest_sessions", "user_external_uid"},
	{"sync_jobs", "uid"}, {"integration_notification_events", "user_external_uid"},
	{"task_intelligence_control", "user_external_uid"}, {"task_recommendation_projections", "user_external_uid"},
	{"task_interventions", "user_external_uid"}, {"task_feedback", "user_external_uid"},
	{"task_outcomes", "user_external_uid"}, {"task_intelligence_snapshots", "user_external_uid"},
	{"hume_expression_jobs", "user_external_uid"},
}

type WipeService struct {
	DB     *sql.DB
	Client *ent.Client
}

func (s WipeService) claim(ctx context.Context, jobID, uid string) error {
	if s.DB == nil {
		return errors.New("account deletion database unavailable")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO account_deletion_wipes(job_id,user_external_uid,status,attempts,created_at,updated_at) VALUES(?,?,?,1,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE status=IF(status='completed','completed','running'),attempts=attempts+1,updated_at=UTC_TIMESTAMP(6)`, jobID, uid, "running")
	return err
}

func (s WipeService) complete(ctx context.Context, jobID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE account_deletion_wipes SET status='completed',completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE job_id=? AND status IN ('running','completed')`, jobID)
	return err
}

func (s WipeService) status(ctx context.Context, jobID string) (string, error) {
	var status string
	err := s.DB.QueryRowContext(ctx, `SELECT status FROM account_deletion_wipes WHERE job_id=?`, jobID).Scan(&status)
	return status, err
}

func (s WipeService) fail(ctx context.Context, jobID string, cause error) {
	if s.DB == nil {
		return
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE account_deletion_wipes SET status='failed',last_error=?,updated_at=UTC_TIMESTAMP(6) WHERE job_id=? AND status='running'`, truncateError(cause), jobID)
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 512 {
		return s[:512]
	}
	return s
}

func (s WipeService) deleteSQLRows(ctx context.Context, uid string) error {
	for _, table := range wipeSQLTables {
		if _, err := s.DB.ExecContext(ctx, "DELETE FROM `"+table.name+"` WHERE `"+table.owner+"`=?", uid); err != nil {
			return err
		}
	}
	return nil
}

func (s WipeService) Run(ctx context.Context, jobID, uid string) error {
	if err := s.claim(ctx, jobID, uid); err != nil {
		return err
	}
	status, err := s.status(ctx, jobID)
	if err != nil {
		return err
	}
	if status == "completed" {
		return nil
	}
	if s.Client != nil {
		if err := (Service{Client: s.Client}).Delete(ctx, uid); err != nil && !errors.Is(err, ErrNotFound) {
			s.fail(ctx, jobID, err)
			return err
		}
	}
	if err := s.deleteSQLRows(ctx, uid); err != nil {
		s.fail(ctx, jobID, err)
		return err
	}
	return s.complete(ctx, jobID)
}

func (h Handler) RunWipe(w http.ResponseWriter, r *http.Request) {
	if !wipeJobAllowed(w, r) {
		return
	}
	var in struct {
		JobID string `json:"job_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in) != nil || strings.TrimSpace(in.JobID) == "" {
		writeWipeJSON(w, http.StatusOK, map[string]string{"status": "dropped", "reason": "invalid_payload"})
		return
	}
	if h.Wipe.DB == nil {
		writeWipeJSON(w, http.StatusOK, map[string]string{"status": "dropped", "reason": "invalid_job"})
		return
	}
	var uid string
	if err := h.Wipe.DB.QueryRowContext(r.Context(), `SELECT user_external_uid FROM account_deletion_wipes WHERE job_id=?`, in.JobID).Scan(&uid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeWipeJSON(w, http.StatusOK, map[string]string{"status": "dropped", "reason": "missing"})
			return
		}
		h.Wipe.fail(r.Context(), in.JobID, err)
		http.Error(w, "deletion job unavailable", http.StatusInternalServerError)
		return
	}
	if err := h.Wipe.Run(r.Context(), in.JobID, strings.TrimSpace(uid)); err != nil {
		http.Error(w, "account deletion wipe failed", http.StatusInternalServerError)
		return
	}
	writeWipeJSON(w, http.StatusOK, map[string]string{"status": "acked", "job_status": "completed"})
}

func wipeJobAllowed(w http.ResponseWriter, r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("INTERNAL_JOB_SECRET"))
	provided := strings.TrimSpace(r.Header.Get("X-Internal-Job-Key"))
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		http.Error(w, "invalid internal job credentials", http.StatusForbidden)
		return false
	}
	return true
}

func writeWipeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
