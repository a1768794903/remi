package workstreams

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"remi/server/internal/auth"
)

type Handler struct{ DB *sql.DB }

func (h Handler) uid(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return "", false
	}
	if h.DB == nil {
		http.Error(w, "workstream storage is not configured", 503)
		return "", false
	}
	return u, true
}
func out(w http.ResponseWriter, s int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s)
	_ = json.NewEncoder(w).Encode(v)
}
func validText(s string, n int) bool {
	return len([]rune(strings.TrimSpace(s))) > 0 && len([]rune(s)) <= n
}

func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	var in struct {
		Title     string     `json:"title"`
		Objective string     `json:"objective"`
		GoalID    *string    `json:"goal_id"`
		Summary   string     `json:"current_state_summary"`
		Review    *time.Time `json:"next_review_at"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || !validText(in.Title, 256) || !validText(in.Objective, 2048) {
		http.Error(w, "invalid workstream", 400)
		return
	}
	id := "ws_" + uuid.NewString()
	now := time.Now().UTC()
	_, e := h.DB.ExecContext(r.Context(), `INSERT INTO workstreams(id,user_external_uid,goal_id,title,objective,status,current_state_summary,next_review_at,last_meaningful_progress_at,latest_event_sequence,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, u, in.GoalID, strings.TrimSpace(in.Title), strings.TrimSpace(in.Objective), "open", in.Summary, in.Review, now, 0, now, now)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	out(w, 201, h.workstream(id, u, in.Title, in.Objective, in.GoalID, "open", in.Summary, in.Review, 0, now, now))
}

func (h Handler) workstream(id, uid, title, obj string, goal *string, status, summary string, review *time.Time, seq int, created, updated time.Time) map[string]any {
	return map[string]any{"workstream_id": id, "goal_id": goal, "title": title, "objective": obj, "status": status, "current_state_summary": summary, "next_review_at": review, "last_meaningful_progress_at": updated, "latest_event_sequence": seq, "created_at": created, "updated_at": updated}
}
func (h Handler) get(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	u, ok := h.uid(w, r)
	if !ok {
		return nil, false
	}
	var id, title, obj, status, summary string
	var goal sql.NullString
	var review sql.NullTime
	var last, created, updated time.Time
	var seq int
	e := h.DB.QueryRowContext(r.Context(), `SELECT id,title,objective,goal_id,status,current_state_summary,next_review_at,last_meaningful_progress_at,latest_event_sequence,created_at,updated_at FROM workstreams WHERE user_external_uid=? AND id=?`, u, r.PathValue("workstream_id")).Scan(&id, &title, &obj, &goal, &status, &summary, &review, &last, &seq, &created, &updated)
	if e == sql.ErrNoRows {
		http.NotFound(w, r)
		return nil, false
	}
	if e != nil {
		http.Error(w, e.Error(), 500)
		return nil, false
	}
	var gp *string
	if goal.Valid {
		gp = &goal.String
	}
	var rp *time.Time
	if review.Valid {
		rp = &review.Time
	}
	return h.workstream(id, u, title, obj, gp, status, summary, rp, seq, created, updated), true
}
func (h Handler) Detail(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.get(w, r)
	if !ok {
		return
	}
	u, _ := auth.UserID(r.Context())
	id := r.PathValue("workstream_id")
	recentEvents := []any{}
	if rows, err := h.DB.QueryContext(r.Context(), `SELECT event_id,sequence,kind,summary,evidence_refs,sensitivity,created_at FROM workstream_events WHERE user_external_uid=? AND workstream_id=? ORDER BY sequence DESC LIMIT 20`, u, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var eventID, kind, summary, sensitivity string
			var sequence int
			var refs []byte
			var created time.Time
			if rows.Scan(&eventID, &sequence, &kind, &summary, &refs, &sensitivity, &created) != nil {
				continue
			}
			var evidence any
			_ = json.Unmarshal(refs, &evidence)
			recentEvents = append(recentEvents, map[string]any{"event_id": eventID, "workstream_id": id, "sequence": sequence, "kind": kind, "summary": summary, "evidence_refs": evidence, "sensitivity": sensitivity, "created_at": created})
		}
	}
	artifacts := []any{}
	if rows, err := h.DB.QueryContext(r.Context(), `SELECT artifact_id,logical_key,version,kind,uri,content_hash,status,created_at FROM workstream_artifacts WHERE user_external_uid=? AND workstream_id=? ORDER BY version DESC LIMIT 50`, u, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var artifactID, logicalKey, kind, uri, contentHash, status string
			var version int
			var created time.Time
			if rows.Scan(&artifactID, &logicalKey, &version, &kind, &uri, &contentHash, &status, &created) != nil {
				continue
			}
			artifacts = append(artifacts, map[string]any{"artifact_id": artifactID, "workstream_id": id, "logical_key": logicalKey, "version": version, "kind": kind, "uri": uri, "content_hash": contentHash, "status": status, "created_at": created})
		}
	}
	checkpoints := []any{}
	if rows, err := h.DB.QueryContext(r.Context(), `SELECT checkpoint_id,runtime_id,last_event_sequence,context_summary,evidence_refs,updated_at FROM workstream_checkpoints WHERE user_external_uid=? AND workstream_id=?`, u, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var checkpointID, runtimeID, summary string
			var sequence int
			var refs []byte
			var updated time.Time
			if rows.Scan(&checkpointID, &runtimeID, &sequence, &summary, &refs, &updated) != nil {
				continue
			}
			var evidence any
			_ = json.Unmarshal(refs, &evidence)
			checkpoints = append(checkpoints, map[string]any{"checkpoint_id": checkpointID, "workstream_id": id, "runtime_id": runtimeID, "last_event_sequence": sequence, "context_summary": summary, "evidence_refs": evidence, "updated_at": updated})
		}
	}
	out(w, 200, map[string]any{"workstream": ws, "recent_events": recentEvents, "tasks": []any{}, "artifacts": artifacts, "checkpoints": checkpoints})
}
func (h Handler) Update(w http.ResponseWriter, r *http.Request) {
	ws, ok := h.get(w, r)
	if !ok {
		return
	}
	var in map[string]any
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	sets := []string{}
	args := []any{}
	for _, k := range []string{"title", "objective", "status", "current_state_summary"} {
		if v, yes := in[k]; yes {
			if s, yes := v.(string); yes {
				sets = append(sets, k+"=?")
				args = append(args, s)
			}
		}
	}
	if v, yes := in["next_review_at"]; yes {
		sets = append(sets, "next_review_at=?")
		args = append(args, v)
	}
	if len(sets) == 0 {
		http.Error(w, "at least one workstream field is required", 422)
		return
	}
	args = append(args, time.Now().UTC(), r.PathValue("workstream_id"), ws["workstream_id"])
	_, e := h.DB.ExecContext(r.Context(), `UPDATE workstreams SET `+strings.Join(sets, ",")+`,updated_at=? WHERE id=? AND id=?`, args...)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	h.Detail(w, r)
}
func (h Handler) Events(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	id := r.PathValue("workstream_id")
	if r.Method == http.MethodGet {
		limit := 100
		rows, e := h.DB.QueryContext(r.Context(), `SELECT event_id,sequence,kind,summary,evidence_refs,sensitivity,created_at FROM workstream_events WHERE user_external_uid=? AND workstream_id=? ORDER BY sequence ASC LIMIT ?`, u, id, limit)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		defer rows.Close()
		a := []map[string]any{}
		for rows.Next() {
			var eid, kind, summary, sens string
			var seq int
			var refs []byte
			var created time.Time
			if rows.Scan(&eid, &seq, &kind, &summary, &refs, &sens, &created) != nil {
				continue
			}
			var er any
			_ = json.Unmarshal(refs, &er)
			a = append(a, map[string]any{"event_id": eid, "workstream_id": id, "sequence": seq, "kind": kind, "summary": summary, "evidence_refs": er, "sensitivity": sens, "created_at": created})
		}
		out(w, 200, a)
		return
	}
	var in struct {
		Kind        string `json:"kind"`
		Summary     string `json:"summary"`
		Evidence    any    `json:"evidence_refs"`
		Sensitivity string `json:"sensitivity"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || !validText(in.Kind, 64) || !validText(in.Summary, 2000) {
		http.Error(w, "invalid event", 400)
		return
	}
	raw, _ := json.Marshal(in.Evidence)
	now := time.Now().UTC()
	var seq int
	if e := h.DB.QueryRowContext(r.Context(), `SELECT COALESCE(MAX(sequence),0)+1 FROM workstream_events WHERE user_external_uid=? AND workstream_id=?`, u, id).Scan(&seq); e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	eid := "wse_" + uuid.NewString()
	_, e := h.DB.ExecContext(r.Context(), `INSERT INTO workstream_events(event_id,user_external_uid,workstream_id,sequence,kind,summary,evidence_refs,sensitivity,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, eid, u, id, seq, in.Kind, in.Summary, raw, in.Sensitivity, now)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	_, _ = h.DB.ExecContext(r.Context(), `UPDATE workstreams SET latest_event_sequence=?,last_meaningful_progress_at=?,updated_at=? WHERE user_external_uid=? AND id=?`, seq, now, now, u, id)
	out(w, 201, map[string]any{"event_id": eid, "workstream_id": id, "sequence": seq, "kind": in.Kind, "summary": in.Summary, "evidence_refs": in.Evidence, "sensitivity": in.Sensitivity, "created_at": now})
}
func (h Handler) Artifacts(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	id := r.PathValue("workstream_id")
	if r.Method == http.MethodGet {
		rows, e := h.DB.QueryContext(r.Context(), `SELECT artifact_id,logical_key,version,kind,uri,content_hash,status,created_at FROM workstream_artifacts WHERE user_external_uid=? AND workstream_id=? ORDER BY version DESC`, u, id)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		defer rows.Close()
		a := []map[string]any{}
		for rows.Next() {
			var id2, key, kind, uri, hash, status string
			var version int
			var created time.Time
			_ = rows.Scan(&id2, &key, &version, &kind, &uri, &hash, &status, &created)
			a = append(a, map[string]any{"artifact_id": id2, "workstream_id": id, "logical_key": key, "version": version, "kind": kind, "uri": uri, "content_hash": hash, "status": status, "created_at": created})
		}
		out(w, 200, a)
		return
	}
	var in struct {
		LogicalKey string `json:"logical_key"`
		Version    int    `json:"version"`
		Kind       string `json:"kind"`
		URI        string `json:"uri"`
		Hash       string `json:"content_hash"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || !validText(in.LogicalKey, 256) || in.Version < 1 || !validText(in.Kind, 64) || !validText(in.URI, 2048) || len(in.Hash) < 16 {
		http.Error(w, "invalid artifact", 400)
		return
	}
	aid := "art_" + uuid.NewString()
	now := time.Now().UTC()
	_, e := h.DB.ExecContext(r.Context(), `INSERT INTO workstream_artifacts(artifact_id,user_external_uid,workstream_id,logical_key,version,kind,uri,content_hash,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, aid, u, id, in.LogicalKey, in.Version, in.Kind, in.URI, in.Hash, "draft", now)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	out(w, 201, map[string]any{"artifact_id": aid, "workstream_id": id, "logical_key": in.LogicalKey, "version": in.Version, "kind": in.Kind, "uri": in.URI, "content_hash": in.Hash, "status": "draft", "created_at": now})
}

func (h Handler) ArtifactStatus(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	allowed := map[string]bool{"draft": true, "awaiting_review": true, "approved": true, "delivered": true, "superseded": true}
	if !allowed[input.Status] {
		http.Error(w, "invalid artifact status", http.StatusUnprocessableEntity)
		return
	}
	artifactID := r.PathValue("artifact_id")
	workstreamID := r.PathValue("workstream_id")
	result, err := h.DB.ExecContext(r.Context(), `UPDATE workstream_artifacts SET status=? WHERE artifact_id=? AND user_external_uid=? AND workstream_id=?`, input.Status, artifactID, u, workstreamID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		http.NotFound(w, r)
		return
	}
	var logicalKey, kind, uri, hash string
	var version int
	var created time.Time
	if err := h.DB.QueryRowContext(r.Context(), `SELECT logical_key,version,kind,uri,content_hash,created_at FROM workstream_artifacts WHERE artifact_id=?`, artifactID).Scan(&logicalKey, &version, &kind, &uri, &hash, &created); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out(w, http.StatusOK, map[string]any{"artifact_id": artifactID, "workstream_id": workstreamID, "logical_key": logicalKey, "version": version, "kind": kind, "uri": uri, "content_hash": hash, "status": input.Status, "created_at": created})
}
func (h Handler) Checkpoints(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	id := r.PathValue("workstream_id")
	if r.Method == http.MethodGet {
		rows, e := h.DB.QueryContext(r.Context(), `SELECT checkpoint_id,runtime_id,last_event_sequence,context_summary,evidence_refs,updated_at FROM workstream_checkpoints WHERE user_external_uid=? AND workstream_id=?`, u, id)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		defer rows.Close()
		a := []map[string]any{}
		for rows.Next() {
			var cid, rid, summary string
			var seq int
			var refs []byte
			var updated time.Time
			_ = rows.Scan(&cid, &rid, &seq, &summary, &refs, &updated)
			a = append(a, map[string]any{"checkpoint_id": cid, "workstream_id": id, "runtime_id": rid, "last_event_sequence": seq, "context_summary": summary, "updated_at": updated})
		}
		out(w, 200, a)
		return
	}
	runtime := r.PathValue("runtime_id")
	var in struct {
		RuntimeID string `json:"runtime_id"`
		Last      int    `json:"last_event_sequence"`
		Summary   string `json:"context_summary"`
		Refs      any    `json:"evidence_refs"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.RuntimeID != runtime || in.Last < 0 {
		http.Error(w, "invalid checkpoint", 422)
		return
	}
	raw, _ := json.Marshal(in.Refs)
	now := time.Now().UTC()
	cid := "cp_" + uuid.NewString()
	_, e := h.DB.ExecContext(r.Context(), `INSERT INTO workstream_checkpoints(checkpoint_id,user_external_uid,workstream_id,runtime_id,last_event_sequence,context_summary,evidence_refs,updated_at) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE last_event_sequence=VALUES(last_event_sequence),context_summary=VALUES(context_summary),evidence_refs=VALUES(evidence_refs),updated_at=VALUES(updated_at)`, cid, u, id, runtime, in.Last, in.Summary, raw, now)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	out(w, 200, map[string]any{"checkpoint_id": cid, "workstream_id": id, "runtime_id": runtime, "last_event_sequence": in.Last, "context_summary": in.Summary, "evidence_refs": in.Refs, "updated_at": now})
}
func (h Handler) Intent(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 256 {
		http.Error(w, "Idempotency-Key is required", 400)
		return
	}
	generation := int64(0)
	if raw := strings.TrimSpace(r.Header.Get("X-Account-Generation")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			http.Error(w, "invalid account generation", 422)
			return
		}
		generation = v
	}
	var in struct {
		Origin                string `json:"origin"`
		TaskID                string `json:"task_id"`
		GoalID                string `json:"goal_id"`
		Title                 string `json:"title"`
		Objective             string `json:"objective"`
		AnchorTaskDescription string `json:"anchor_task_description"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil {
		http.Error(w, "invalid work intent", 422)
		return
	}
	if in.Origin != "task" && in.Origin != "goal" {
		http.Error(w, "origin must be task or goal", 422)
		return
	}
	now := time.Now().UTC()
	receiptID := "intent_" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(u+":"+strconv.FormatInt(generation, 10)+":"+key)).String()
	var oldHash string
	raw, _ := json.Marshal(in)
	sum := uuid.NewSHA1(uuid.NameSpaceURL, raw).String()
	if err := h.DB.QueryRowContext(r.Context(), `SELECT request_hash FROM work_intent_receipts WHERE user_external_uid=? AND account_generation=? AND idempotency_key=?`, u, generation, key).Scan(&oldHash); err == nil {
		if oldHash != sum {
			http.Error(w, "idempotency key was reused with another intent", 409)
			return
		}
		var wsID, taskID, goalID string
		var created time.Time
		if e := h.DB.QueryRowContext(r.Context(), `SELECT workstream_id,task_id,COALESCE(goal_external_id,''),created_at FROM work_intent_receipts WHERE user_external_uid=? AND account_generation=? AND idempotency_key=?`, u, generation, key).Scan(&wsID, &taskID, &goalID, &created); e == nil {
			out(w, 200, map[string]any{"receipt_id": receiptID, "workstream_id": wsID, "task_id": taskID, "goal_id": goalID, "newly_created": false, "created_at": created})
			return
		}
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "workstream storage unavailable", 503)
		return
	}
	defer tx.Rollback()
	var wsID, taskID, goalID, title, objective string
	newly := false
	if in.Origin == "task" {
		if _, err = strconv.ParseInt(in.TaskID, 10, 64); err != nil {
			http.Error(w, "task not found", 404)
			return
		}
		var existing sql.NullString
		if err = tx.QueryRowContext(r.Context(), `SELECT CAST(ai.id AS CHAR),ai.description,COALESCE(ai.workstream_id,'') FROM action_items ai JOIN users u ON u.id=ai.user_id WHERE u.external_uid=? AND ai.id=?`, u, in.TaskID).Scan(&taskID, &objective, &existing); err != nil {
			http.NotFound(w, r)
			return
		}
		wsID = existing.String
		title = strings.TrimSpace(in.Title)
		if title == "" {
			title = objective
		}
		if strings.TrimSpace(in.Objective) != "" {
			objective = in.Objective
		}
		if wsID == "" {
			wsID = "ws_" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(u+":"+in.TaskID)).String()
			newly = true
			if _, err = tx.ExecContext(r.Context(), `INSERT INTO workstreams(id,user_external_uid,title,objective,status,current_state_summary,next_review_at,last_meaningful_progress_at,latest_event_sequence,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, wsID, u, title, objective, "open", "", nil, now, 0, now, now); err != nil {
				http.Error(w, "failed to create workstream", 503)
				return
			}
			if _, err = tx.ExecContext(r.Context(), `UPDATE action_items SET workstream_id=? WHERE id=?`, wsID, in.TaskID); err != nil {
				http.Error(w, "failed to link task", 503)
				return
			}
		}
	} else {
		goalID = strings.TrimSpace(in.GoalID)
		if goalID == "" || in.Title == "" || in.Objective == "" || in.AnchorTaskDescription == "" {
			http.Error(w, "invalid goal work intent", 422)
			return
		}
		var goalStatus string
		if err = tx.QueryRowContext(r.Context(), `SELECT status FROM goals g JOIN users u ON u.id=g.user_id WHERE u.external_uid=? AND g.external_id=?`, u, goalID).Scan(&goalStatus); err != nil {
			http.NotFound(w, r)
			return
		}
		if goalStatus == "achieved" || goalStatus == "abandoned" {
			http.Error(w, "ended goal cannot receive new work", 409)
			return
		}
		wsID = "ws_" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(u+":"+goalID+":"+key)).String()
		taskID = "intent-task_" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(wsID)).String()
		title = in.Title
		objective = in.Objective
		newly = true
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO workstreams(id,user_external_uid,goal_id,title,objective,status,current_state_summary,next_review_at,last_meaningful_progress_at,latest_event_sequence,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, wsID, u, goalID, title, objective, "open", "", nil, now, 0, now, now); err != nil {
			http.Error(w, "failed to create workstream", 503)
			return
		}
		var userID int64
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM users WHERE external_uid=?`, u).Scan(&userID); err != nil {
			http.Error(w, "user not found", 404)
			return
		}
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO action_items(description,status,owner,source,created_at,updated_at,user_id,workstream_id,goal_external_id) VALUES(?,?,?,?,?,?,?,?,?)`, in.AnchorTaskDescription, "active", "user", "explicit_goal_intent", now, now, userID, wsID, goalID); err != nil {
			http.Error(w, "failed to create task", 503)
			return
		}
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO work_intent_receipts(receipt_id,user_external_uid,account_generation,idempotency_key,request_hash,workstream_id,task_id,goal_external_id,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, receiptID, u, generation, key, sum, wsID, taskID, goalID, now); err != nil {
		http.Error(w, "failed to store work intent", 503)
		return
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, "failed to commit work intent", 503)
		return
	}
	out(w, 201, map[string]any{"receipt_id": receiptID, "workstream_id": wsID, "task_id": taskID, "goal_id": goalID, "newly_created": newly, "created_at": now})
}
