package candidates

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"remi/server/internal/actionitems"
	"remi/server/internal/auth"
)

type Handler struct {
	DB      *sql.DB
	Actions actionitems.Service
}

type candidate struct {
	ID                  string          `json:"candidate_id"`
	UID                 string          `json:"-"`
	SubjectKind         string          `json:"subject_kind"`
	ProposedAction      string          `json:"proposed_action"`
	TaskID              *string         `json:"task_id,omitempty"`
	TaskChange          json.RawMessage `json:"task_change,omitempty"`
	WorkstreamProposal  json.RawMessage `json:"workstream_proposal,omitempty"`
	CaptureConfidence   float64         `json:"capture_confidence"`
	OwnershipConfidence float64         `json:"ownership_confidence"`
	GoalID              *string         `json:"goal_id,omitempty"`
	WorkstreamID        *string         `json:"workstream_id,omitempty"`
	EvidenceRefs        json.RawMessage `json:"evidence_refs"`
	SourceSurface       string          `json:"source_surface"`
	Compatibility       json.RawMessage `json:"compatibility,omitempty"`
	Status              string          `json:"status"`
	AccountGeneration   int64           `json:"account_generation"`
	IdempotencyKey      string          `json:"idempotency_key"`
	ResolutionReason    *string         `json:"resolution_reason,omitempty"`
	ResultTaskID        *string         `json:"result_task_id,omitempty"`
	ResultWorkstreamID  *string         `json:"result_workstream_id,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	ResolvedAt          *time.Time      `json:"resolved_at,omitempty"`
	ExpiresAt           *time.Time      `json:"expires_at,omitempty"`
}

func (h Handler) user(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return "", false
	}
	if h.DB == nil {
		http.Error(w, "candidate storage is not configured", http.StatusServiceUnavailable)
		return "", false
	}
	return u, true
}
func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func generation(r *http.Request) (int64, error) {
	v := r.Header.Get("X-Account-Generation")
	if v == "" {
		return 0, nil
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n < 0 {
		return 0, errors.New("invalid account generation")
	}
	return n, nil
}
func idem(r *http.Request) string { return strings.TrimSpace(r.Header.Get("Idempotency-Key")) }
func (h Handler) scan(row *sql.Row) (candidate, error) {
	var c candidate
	var taskID, goalID, wsID, reason, resultTask, resultWS sql.NullString
	var taskChange, wsProposal, refs, compat []byte
	err := row.Scan(&c.ID, &c.UID, &c.SubjectKind, &c.ProposedAction, &taskID, &taskChange, &wsProposal, &c.CaptureConfidence, &c.OwnershipConfidence, &goalID, &wsID, &refs, &c.SourceSurface, &compat, &c.Status, &c.AccountGeneration, &c.IdempotencyKey, &reason, &resultTask, &resultWS, &c.CreatedAt, &c.ResolvedAt, &c.ExpiresAt)
	if err != nil {
		return c, err
	}
	if taskID.Valid {
		c.TaskID = &taskID.String
	}
	if goalID.Valid {
		c.GoalID = &goalID.String
	}
	if wsID.Valid {
		c.WorkstreamID = &wsID.String
	}
	if reason.Valid {
		c.ResolutionReason = &reason.String
	}
	if resultTask.Valid {
		c.ResultTaskID = &resultTask.String
	}
	if resultWS.Valid {
		c.ResultWorkstreamID = &resultWS.String
	}
	c.TaskChange = taskChange
	c.WorkstreamProposal = wsProposal
	c.EvidenceRefs = refs
	c.Compatibility = compat
	return c, nil
}

const selectCandidate = `SELECT candidate_id,user_external_uid,subject_kind,proposed_action,task_id,task_change,workstream_proposal,capture_confidence,ownership_confidence,goal_id,workstream_id,evidence_refs,source_surface,compatibility,status,account_generation,idempotency_key,resolution_reason,result_task_id,result_workstream_id,created_at,resolved_at,expires_at FROM candidates`

func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(w, r)
	if !ok {
		return
	}
	g, err := generation(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	key := idem(r)
	if key == "" || len(key) > 512 {
		http.Error(w, "Idempotency-Key is required", 400)
		return
	}
	var in struct {
		SubjectKind         string          `json:"subject_kind"`
		ProposedAction      string          `json:"proposed_action"`
		TaskID              *string         `json:"task_id"`
		TaskChange          json.RawMessage `json:"task_change"`
		WorkstreamProposal  json.RawMessage `json:"workstream_proposal"`
		CaptureConfidence   float64         `json:"capture_confidence"`
		OwnershipConfidence float64         `json:"ownership_confidence"`
		GoalID              *string         `json:"goal_id"`
		WorkstreamID        *string         `json:"workstream_id"`
		EvidenceRefs        json.RawMessage `json:"evidence_refs"`
		SourceSurface       string          `json:"source_surface"`
		Compatibility       json.RawMessage `json:"compatibility"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || (in.SubjectKind != "task" && in.SubjectKind != "workstream") || in.ProposedAction == "" || in.CaptureConfidence < 0 || in.CaptureConfidence > 1 || in.OwnershipConfidence < 0 || in.OwnershipConfidence > 1 || len(in.EvidenceRefs) == 0 || strings.TrimSpace(in.SourceSurface) == "" {
		http.Error(w, "invalid candidate", 400)
		return
	}
	var existing candidate
	existing, e := h.scan(h.DB.QueryRowContext(r.Context(), selectCandidate+` WHERE user_external_uid=? AND account_generation=? AND idempotency_key=?`, u, g, key))
	if e == nil {
		jsonOut(w, 200, existing)
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		http.Error(w, e.Error(), 500)
		return
	}
	id := "cand_" + uuid.NewString()
	now := time.Now().UTC()
	expires := now.Add(7 * 24 * time.Hour)
	_, e = h.DB.ExecContext(r.Context(), `INSERT INTO candidates(candidate_id,user_external_uid,subject_kind,proposed_action,task_id,task_change,workstream_proposal,capture_confidence,ownership_confidence,goal_id,workstream_id,evidence_refs,source_surface,compatibility,status,account_generation,idempotency_key,created_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, u, in.SubjectKind, in.ProposedAction, in.TaskID, in.TaskChange, in.WorkstreamProposal, in.CaptureConfidence, in.OwnershipConfidence, in.GoalID, in.WorkstreamID, in.EvidenceRefs, in.SourceSurface, in.Compatibility, "pending", g, key, now, expires)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	c := candidate{ID: id, UID: u, SubjectKind: in.SubjectKind, ProposedAction: in.ProposedAction, TaskID: in.TaskID, TaskChange: in.TaskChange, WorkstreamProposal: in.WorkstreamProposal, CaptureConfidence: in.CaptureConfidence, OwnershipConfidence: in.OwnershipConfidence, GoalID: in.GoalID, WorkstreamID: in.WorkstreamID, EvidenceRefs: in.EvidenceRefs, SourceSurface: in.SourceSurface, Compatibility: in.Compatibility, Status: "pending", AccountGeneration: g, IdempotencyKey: key, CreatedAt: now, ExpiresAt: &expires}
	jsonOut(w, 201, c)
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(w, r)
	if !ok {
		return
	}
	g, e := generation(r)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 500 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	status := r.URL.Query().Get("status")
	q := selectCandidate + ` WHERE user_external_uid=? AND account_generation=?`
	args := []any{u, g}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit+1, offset)
	rows, e := h.DB.QueryContext(r.Context(), q, args...)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	defer rows.Close()
	items := []candidate{}
	for rows.Next() {
		c, er := h.scanRows(rows)
		if er != nil {
			http.Error(w, er.Error(), 500)
			return
		}
		items = append(items, c)
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	jsonOut(w, 200, map[string]any{"candidates": items, "has_more": more})
}
func (h Handler) scanRows(rows *sql.Rows) (candidate, error) {
	var c candidate
	var taskID, goalID, wsID, reason, resultTask, resultWS sql.NullString
	var tc, wp, refs, compat []byte
	err := rows.Scan(&c.ID, &c.UID, &c.SubjectKind, &c.ProposedAction, &taskID, &tc, &wp, &c.CaptureConfidence, &c.OwnershipConfidence, &goalID, &wsID, &refs, &c.SourceSurface, &compat, &c.Status, &c.AccountGeneration, &c.IdempotencyKey, &reason, &resultTask, &resultWS, &c.CreatedAt, &c.ResolvedAt, &c.ExpiresAt)
	if err != nil {
		return c, err
	}
	if taskID.Valid {
		c.TaskID = &taskID.String
	}
	if goalID.Valid {
		c.GoalID = &goalID.String
	}
	if wsID.Valid {
		c.WorkstreamID = &wsID.String
	}
	if reason.Valid {
		c.ResolutionReason = &reason.String
	}
	if resultTask.Valid {
		c.ResultTaskID = &resultTask.String
	}
	if resultWS.Valid {
		c.ResultWorkstreamID = &resultWS.String
	}
	c.TaskChange = tc
	c.WorkstreamProposal = wp
	c.EvidenceRefs = refs
	c.Compatibility = compat
	return c, nil
}
func (h Handler) Detail(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(w, r)
	if !ok {
		return
	}
	g, e := generation(r)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	c, e := h.scan(h.DB.QueryRowContext(r.Context(), selectCandidate+` WHERE user_external_uid=? AND candidate_id=? AND account_generation=?`, u, r.PathValue("candidate_id"), g))
	if errors.Is(e, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	jsonOut(w, 200, c)
}
func (h Handler) Resolve(w http.ResponseWriter, r *http.Request) {
	u, ok := h.user(w, r)
	if !ok {
		return
	}
	g, e := generation(r)
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	id := r.PathValue("candidate_id")
	action := r.PathValue("resolution")
	if action == "accept" {
		action = "accepted"
	}
	if action != "accepted" && action != "rejected" && action != "expired" {
		http.Error(w, "invalid resolution", 400)
		return
	}
	tx, e := h.DB.BeginTx(r.Context(), nil)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	defer tx.Rollback()
	var c candidate // Lock by selecting the canonical row.
	row := tx.QueryRowContext(r.Context(), selectCandidate+` WHERE user_external_uid=? AND candidate_id=? AND account_generation=? FOR UPDATE`, u, id, g)
	c, e = h.scan(row)
	if errors.Is(e, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	if c.Status == action {
		jsonOut(w, 200, map[string]any{"candidate_id": id, "status": action, "newly_resolved": false, "resolved_at": c.ResolvedAt})
		return
	}
	if c.Status != "pending" {
		http.Error(w, "candidate already resolved", 409)
		return
	}
	now := time.Now().UTC()
	var resultTask *string
	if action == "accepted" && c.ProposedAction == "create" && c.SubjectKind == "task" {
		var task struct {
			Description string     `json:"description"`
			DueAt       *time.Time `json:"due_at"`
		}
		if json.Unmarshal(c.TaskChange, &task) == nil && strings.TrimSpace(task.Description) != "" {
			item, er := h.Actions.Create(r.Context(), u, actionitems.CreateInput{Description: task.Description, Status: "active", Owner: "user", Source: "candidate", DueAt: task.DueAt})
			if er != nil {
				http.Error(w, er.Error(), 500)
				return
			}
			resultTask = &item.ID
		}
	}
	_, e = tx.ExecContext(r.Context(), `UPDATE candidates SET status=?,resolution_reason=?,result_task_id=?,resolved_at=?,expires_at=NULL WHERE user_external_uid=? AND candidate_id=? AND status='pending'`, action, action, resultTask, now, u, id)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	if e = tx.Commit(); e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	receipt := sha256.Sum256([]byte(id + ":" + strconv.FormatInt(g, 10) + ":" + action))
	jsonOut(w, 200, map[string]any{"candidate_id": id, "status": action, "receipt_id": hex.EncodeToString(receipt[:]), "task_id": resultTask, "newly_resolved": true, "resolved_at": now})
}
func (h Handler) Control(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.user(w, r); !ok {
		return
	}
	jsonOut(w, 200, map[string]any{"workflow_mode": "off", "account_generation": 0, "chat_first_ui": false})
}
func (h Handler) Migrate(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.user(w, r); !ok {
		return
	}
	jsonOut(w, 200, map[string]any{"migrated": 0, "skipped": 0, "has_more": false, "next_cursor": nil})
}
func (h Handler) Drain(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.user(w, r); !ok {
		return
	}
	jsonOut(w, 200, map[string]int{"scheduled": 0})
}
