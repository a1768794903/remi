package taskintelligence

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/auth"
)

// Handler implements the durable task-intelligence contract. Recommendation
// selection is deliberately deterministic at this layer; a provider can later
// enrich the projection without changing the storage or attribution contract.
type Handler struct{ DB *sql.DB }

type recommendation struct {
	InterventionID      string           `json:"intervention_id"`
	OutputVersion       string           `json:"output_version"`
	SubjectKind         string           `json:"subject_kind"`
	SubjectID           string           `json:"subject_id"`
	FeedbackSubjectKind string           `json:"feedback_subject_kind"`
	FeedbackSubjectID   string           `json:"feedback_subject_id"`
	Headline            string           `json:"headline"`
	WhyNow              string           `json:"why_now"`
	RecommendedAction   string           `json:"recommended_action"`
	AlternativeAction   *string          `json:"alternative_action,omitempty"`
	EvidencePreview     string           `json:"evidence_preview"`
	EvidenceRefs        []map[string]any `json:"evidence_refs"`
	DedupeKey           string           `json:"dedupe_key"`
	ExpiresAt           time.Time        `json:"expires_at"`
}

type projection struct {
	SchemaVersion   int              `json:"schema_version"`
	EvaluationID    string           `json:"evaluation_id"`
	OutputVersion   string           `json:"output_version"`
	MaterialVersion string           `json:"material_version"`
	GeneratedAt     time.Time        `json:"generated_at"`
	ExpiresAt       time.Time        `json:"expires_at"`
	Recommendations []recommendation `json:"recommendations"`
}

func uid(r *http.Request) (string, bool) {
	u, err := auth.UserID(r.Context())
	if err != nil {
		return "", false
	}
	return u, true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func requestJSON(r *http.Request, out any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(out)
}

func generation(r *http.Request) (int64, error) {
	v := strings.TrimSpace(r.Header.Get("X-Account-Generation"))
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("invalid account generation")
	}
	return n, nil
}

func requireMutation(r *http.Request) (int64, string, error) {
	g, err := generation(r)
	if err != nil {
		return 0, "", err
	}
	k := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if k == "" || len(k) > 512 {
		return 0, "", errors.New("Idempotency-Key is required")
	}
	return g, k, nil
}

func stable(prefix string, parts ...any) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%v\x1f", p)
	}
	return prefix + "_" + hex.EncodeToString(h.Sum(nil))[:32]
}

func (h Handler) check(w http.ResponseWriter) bool {
	if h.DB == nil {
		http.Error(w, "task intelligence storage is not configured", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (h Handler) accountGeneration(r *http.Request, uid string, requested int64) error {
	var actual int64
	err := h.DB.QueryRowContext(r.Context(), `SELECT account_generation FROM task_intelligence_control WHERE user_external_uid=?`, uid).Scan(&actual)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if actual != requested {
		return errors.New("account generation mismatch")
	}
	return nil
}

func (h Handler) Evaluate(w http.ResponseWriter, r *http.Request) {
	if !h.check(w) {
		return
	}
	u, ok := uid(r)
	if !ok {
		http.Error(w, "missing authenticated user", 401)
		return
	}
	if r.Method == http.MethodPost {
		var in struct {
			DeviceID     string `json:"device_id"`
			MaterialHint string `json:"material_hint"`
		}
		if err := requestJSON(r, &in); err != nil && !errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "invalid request", 422)
			return
		}
	}
	g, err := generation(r)
	if err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	if err = h.accountGeneration(r, u, g); err != nil {
		if strings.Contains(err.Error(), "mismatch") {
			http.Error(w, err.Error(), 409)
		} else {
			http.Error(w, err.Error(), 503)
		}
		return
	}
	now := time.Now().UTC()
	expires := now.Add(15 * time.Minute)
	rows, err := h.DB.QueryContext(r.Context(), `SELECT CAST(ai.id AS CHAR),ai.description,COALESCE(ai.due_at, ai.updated_at) FROM action_items ai JOIN users u ON u.id=ai.user_id WHERE u.external_uid=? AND ai.status='active' ORDER BY ai.due_at IS NULL,ai.due_at,ai.updated_at DESC LIMIT 3`, u)
	if err != nil {
		http.Error(w, "failed to evaluate task intelligence", 503)
		return
	}
	defer rows.Close()
	items := make([]recommendation, 0, 3)
	for rows.Next() {
		var id, description string
		var due time.Time
		if err := rows.Scan(&id, &description, &due); err != nil {
			http.Error(w, "failed to evaluate task intelligence", 503)
			return
		}
		eid := stable("intervention", u, g, id)
		dedupe := stable("dedupe", u, id)
		items = append(items, recommendation{InterventionID: eid, OutputVersion: "task-intelligence-v1", SubjectKind: "task", SubjectID: id, FeedbackSubjectKind: "task", FeedbackSubjectID: id, Headline: description, WhyNow: "This active task is the next due item in your personal task list.", RecommendedAction: "do_now", EvidencePreview: description, EvidenceRefs: []map[string]any{{"kind": "action_item", "id": id}}, DedupeKey: dedupe, ExpiresAt: expires})
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "failed to evaluate task intelligence", 503)
		return
	}
	p := projection{SchemaVersion: 1, EvaluationID: stable("evaluation", u, g, now.UnixNano()), OutputVersion: "task-intelligence-v1", MaterialVersion: stable("material", u, g, now.UnixNano()/int64(time.Minute)), GeneratedAt: now, ExpiresAt: expires, Recommendations: items}
	raw, _ := json.Marshal(p)
	_, _ = h.DB.ExecContext(r.Context(), `INSERT INTO task_recommendation_projections(user_external_uid,account_generation,evaluation_id,device_scope,payload,generated_at,expires_at) VALUES(?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE payload=VALUES(payload),evaluation_id=VALUES(evaluation_id),generated_at=VALUES(generated_at),expires_at=VALUES(expires_at)`, u, g, p.EvaluationID, r.URL.Query().Get("device_id"), raw, now, expires)
	writeJSON(w, 200, p)
}

type interventionInput struct {
	Surface      string           `json:"surface"`
	SubjectKind  string           `json:"subject_kind"`
	SubjectID    string           `json:"subject_id"`
	DedupeKey    string           `json:"dedupe_key"`
	EvidenceRefs []map[string]any `json:"evidence_refs"`
	ExpiresAt    time.Time        `json:"expires_at"`
}

func (h Handler) Intervention(w http.ResponseWriter, r *http.Request) {
	h.mutation(w, r, "intervention")
}
func (h Handler) Feedback(w http.ResponseWriter, r *http.Request) { h.mutation(w, r, "feedback") }
func (h Handler) Outcome(w http.ResponseWriter, r *http.Request)  { h.mutation(w, r, "outcome") }

func (h Handler) mutation(w http.ResponseWriter, r *http.Request, kind string) {
	if !h.check(w) {
		return
	}
	u, ok := uid(r)
	if !ok {
		http.Error(w, "missing authenticated user", 401)
		return
	}
	g, key, err := requireMutation(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err = h.accountGeneration(r, u, g); err != nil {
		if strings.Contains(err.Error(), "mismatch") {
			http.Error(w, err.Error(), 409)
		} else {
			http.Error(w, err.Error(), 503)
		}
		return
	}
	var body map[string]any
	if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "invalid request", 422)
		return
	}
	raw, _ := json.Marshal(body)
	hash := sha256.Sum256(raw)
	id := stable(kind, u, g, key)
	now := time.Now().UTC()
	if kind == "intervention" {
		var in interventionInput
		if json.Unmarshal(raw, &in) != nil || in.SubjectKind == "" || in.SubjectID == "" || in.DedupeKey == "" || in.Surface == "" {
			http.Error(w, "invalid intervention", 422)
			return
		}
		chain := stable("attr", u, g, id)
		evidence, _ := json.Marshal(in.EvidenceRefs)
		_, err = h.DB.ExecContext(r.Context(), `INSERT INTO task_interventions(intervention_id,user_external_uid,account_generation,idempotency_key,request_hash,attribution_chain_id,surface,subject_kind,subject_id,dedupe_key,evidence_refs,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE intervention_id=intervention_id`, id, u, g, key, hex.EncodeToString(hash[:]), chain, in.Surface, in.SubjectKind, in.SubjectID, in.DedupeKey, evidence, in.ExpiresAt, now)
		if err != nil {
			http.Error(w, "failed to store intervention", 503)
			return
		}
		writeJSON(w, 200, map[string]any{"intervention_id": id, "attribution_chain_id": chain, "surface": in.Surface, "subject_kind": in.SubjectKind, "subject_id": in.SubjectID, "dedupe_key": in.DedupeKey, "evidence_refs": in.EvidenceRefs, "expires_at": in.ExpiresAt, "created_at": now})
		return
	}
	if kind == "feedback" {
		if body["subject_kind"] == nil || body["subject_id"] == nil || body["action"] == nil {
			http.Error(w, "invalid feedback", 422)
			return
		}
		chain := stable("attr", u, g, fmt.Sprint(body["subject_kind"]), fmt.Sprint(body["subject_id"]))
		_, err = h.DB.ExecContext(r.Context(), `INSERT INTO task_feedback(feedback_id,user_external_uid,account_generation,idempotency_key,request_hash,attribution_chain_id,payload,created_at) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE feedback_id=feedback_id`, id, u, g, key, hex.EncodeToString(hash[:]), chain, raw, now)
		if err != nil {
			http.Error(w, "failed to store feedback", 503)
			return
		}
		body["feedback_id"] = id
		body["attribution_chain_id"] = chain
		body["created_at"] = now
		writeJSON(w, 200, body)
		return
	}
	if kind == "outcome" {
		if body["attribution_chain_id"] == nil || body["subject_kind"] == nil || body["subject_id"] == nil || body["outcome_code"] == nil {
			http.Error(w, "invalid outcome", 422)
			return
		}
		var n int
		err = h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM task_interventions WHERE user_external_uid=? AND attribution_chain_id=? AND account_generation=?`, u, body["attribution_chain_id"], g).Scan(&n)
		if err != nil || n == 0 {
			http.Error(w, "attribution chain not found", 404)
			return
		}
		_, err = h.DB.ExecContext(r.Context(), `INSERT INTO task_outcomes(outcome_id,user_external_uid,account_generation,idempotency_key,request_hash,attribution_chain_id,payload,occurred_at) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE outcome_id=outcome_id`, id, u, g, key, hex.EncodeToString(hash[:]), body["attribution_chain_id"], raw, now)
		if err != nil {
			http.Error(w, "failed to store outcome", 503)
			return
		}
		body["outcome_id"] = id
		body["occurred_at"] = now
		writeJSON(w, 200, body)
		return
	}
}

func (h Handler) Snapshot(w http.ResponseWriter, r *http.Request) {
	if !h.check(w) {
		return
	}
	u, ok := uid(r)
	if !ok {
		http.Error(w, "missing authenticated user", 401)
		return
	}
	g, key, err := requireMutation(r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err = h.accountGeneration(r, u, g); err != nil {
		if strings.Contains(err.Error(), "mismatch") {
			http.Error(w, err.Error(), 409)
		} else {
			http.Error(w, err.Error(), 503)
		}
		return
	}
	var body map[string]any
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body) != nil {
		http.Error(w, "invalid snapshot", 422)
		return
	}
	raw, _ := json.Marshal(body)
	hash := sha256.Sum256(raw)
	kind := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/task-intelligence/"), "-snapshot")
	if kind != "context" && kind != "open-loop" {
		http.Error(w, "unknown snapshot", 404)
		return
	}
	id := stable("snapshot", u, g, kind, fmt.Sprint(body["device_id"]))
	now := time.Now().UTC()
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO task_intelligence_snapshots(snapshot_id,user_external_uid,account_generation,snapshot_kind,idempotency_key,request_hash,payload,generated_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE payload=VALUES(payload),generated_at=VALUES(generated_at),expires_at=VALUES(expires_at)`, id, u, g, kind, key, hex.EncodeToString(hash[:]), raw, now, now.Add(30*time.Minute))
	if err != nil {
		http.Error(w, "failed to store snapshot", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"snapshot_id": fmt.Sprint(body["snapshot_id"]), "replaced": true, "expires_at": now.Add(30 * time.Minute)})
}
