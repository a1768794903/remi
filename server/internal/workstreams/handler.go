package workstreams

import (
	"database/sql"
	"encoding/json"
	"net/http"
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
	out(w, 200, map[string]any{"workstream": ws, "recent_events": []any{}, "tasks": []any{}, "artifacts": []any{}, "checkpoints": []any{}})
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
	if _, ok := h.uid(w, r); !ok {
		return
	}
	http.Error(w, "work intent requires canonical task integration", http.StatusServiceUnavailable)
}
