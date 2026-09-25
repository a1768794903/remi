package memories

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"remi/server/internal/auth"
	"remi/server/internal/chat"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Handler struct {
	Service  Service
	DB       *sql.DB
	Provider chat.Provider
}

const maxBatchCreateMemories = 200

func (h Handler) Extract(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.Provider == nil {
		http.Error(w, "memory extraction provider is not configured", http.StatusServiceUnavailable)
		return
	}
	var in struct {
		Text       string   `json:"text"`
		TextSource string   `json:"text_source"`
		Existing   []string `json:"existing_memories"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Text) == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	if len([]rune(in.Text)) > 50000 {
		http.Error(w, "text is too long", http.StatusBadRequest)
		return
	}
	if len(in.Existing) > 200 {
		http.Error(w, "existing_memories exceeds 200 items", http.StatusBadRequest)
		return
	}
	source := strings.TrimSpace(in.TextSource)
	if source == "" {
		source = "memory_log"
	}
	existing := make([]string, 0, len(in.Existing))
	for _, item := range in.Existing {
		if item = strings.TrimSpace(item); item != "" {
			if len([]rune(item)) > 4096 {
				item = string([]rune(item)[:4096])
			}
			existing = append(existing, item)
		}
	}
	prompt := "Extract durable user memories from the text. Return JSON only with keys memories (array of concise strings) and profile (string). Do not invent facts. Text source: " + source + "\nExisting memories:\n" + strings.Join(existing, "\n") + "\nText:\n" + in.Text
	raw, err := h.Provider.Complete(r.Context(), []chat.Turn{{Role: "system", Content: "You extract user memories into strict JSON."}, {Role: "user", Content: prompt}})
	if err != nil {
		http.Error(w, "memories_extract_failed", http.StatusBadGateway)
		return
	}
	result, err := ParseExtractResult(raw)
	if err != nil {
		http.Error(w, "memories_extract_failed", http.StatusBadGateway)
		return
	}
	_ = uid
	_ = json.NewEncoder(w).Encode(result)
}

func validateBatchCreateCount(count int) error {
	if count < 0 || count > maxBatchCreateMemories {
		return errors.New("memory batch exceeds the maximum of 200 items")
	}
	return nil
}

func (h Handler) ProductSearch(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	result, err := h.Service.Search(r.Context(), uid, r.URL.Query().Get("query"), limit, offset, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func (h Handler) VectorSearch(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	result, err := h.Service.Search(r.Context(), uid, r.URL.Query().Get("query"), 10, 0, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"memories": result.Items, "total": result.Total, "source": "mysql_lexical_fallback", "vector_available": false})
}

func (h Handler) ArchiveSearch(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("include_archive") != "true" {
		http.Error(w, "explicit archive capability is required", http.StatusForbidden)
		return
	}
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	result, err := h.Service.Search(r.Context(), uid, r.URL.Query().Get("query"), limit, offset, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func (h Handler) Batch(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var input struct {
		MemoryIDs []string `json:"memory_ids"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.MemoryIDs) == 0 {
		http.Error(w, "memory_ids are required", http.StatusBadRequest)
		return
	}
	count, err := h.Service.DeleteBatch(r.Context(), uid, input.MemoryIDs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "deleted_count": count})
}

func (h Handler) DeleteAll(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	count, err := h.Service.DeleteAll(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "deleted_count": count})
}

func (h Handler) Visibility(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var input struct {
		Value string `json:"value"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || (input.Value != "private" && input.Value != "public") {
		http.Error(w, "invalid visibility", http.StatusBadRequest)
		return
	}
	item, err := h.Service.Update(r.Context(), uid, r.PathValue("memory_id"), UpdateInput{Visibility: &input.Value})
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "memory": item})
}

func (h Handler) Read(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var input struct {
		IsRead      *bool `json:"is_read"`
		IsDismissed *bool `json:"is_dismissed"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || (input.IsRead == nil && input.IsDismissed == nil) {
		http.Error(w, "read mutation is required", http.StatusBadRequest)
		return
	}
	item, err := h.Service.Update(r.Context(), uid, r.PathValue("memory_id"), UpdateInput{IsRead: input.IsRead, IsDismissed: input.IsDismissed})
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(item)
}

func (h Handler) Collection(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if r.Method == http.MethodPost {
		var in CreateInput
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		item, err := h.Service.Create(r.Context(), uid, in)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(item)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, err := h.Service.List(r.Context(), uid, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(items)
}

func (h Handler) BatchCreate(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var input struct {
		Memories []CreateInput `json:"memories"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := validateBatchCreateCount(len(input.Memories)); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	created := make([]Item, 0, len(input.Memories))
	for _, memory := range input.Memories {
		item, createErr := h.Service.Create(r.Context(), uid, memory)
		if createErr != nil {
			http.Error(w, createErr.Error(), http.StatusBadRequest)
			return
		}
		created = append(created, item)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"memories": created, "created_count": len(created)})
}

type memoryUseRequest struct {
	Action               string `json:"action"`
	FeedbackID           string `json:"feedback_id"`
	ExpectedItemRevision *int64 `json:"expected_item_revision"`
}

type memoryUseResponse struct {
	Status         string          `json:"status"`
	MemoryID       string          `json:"memory_id"`
	Action         MemoryUseAction `json:"action"`
	FeedbackID     string          `json:"feedback_id"`
	ItemRevision   int64           `json:"item_revision"`
	Suppressed     bool            `json:"suppressed"`
	CurationWeight int             `json:"curation_weight"`
}

func (h Handler) Use(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.DB == nil {
		http.Error(w, "memory canonical storage is not configured", http.StatusServiceUnavailable)
		return
	}
	var in memoryUseRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	action, err := NormalizeMemoryUseAction(in.Action)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	feedbackID, err := NormalizeFeedbackID(in.FeedbackID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if in.ExpectedItemRevision != nil && *in.ExpectedItemRevision < 1 {
		http.Error(w, "expected_item_revision must be positive", http.StatusBadRequest)
		return
	}

	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	id := r.PathValue("memory_id")
	var revision int64
	var weight int
	var rawState []byte
	if err = tx.QueryRowContext(r.Context(), `SELECT m.item_revision,m.curation_weight,m.memory_use FROM memories m JOIN users u ON u.id=m.user_id WHERE m.id=? AND u.external_uid=? FOR UPDATE`, id, uid).Scan(&revision, &weight, &rawState); errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if in.ExpectedItemRevision != nil && *in.ExpectedItemRevision != revision {
		http.Error(w, "memory revision has changed", http.StatusConflict)
		return
	}
	var existing MemoryUseState
	if len(rawState) > 0 {
		_ = json.Unmarshal(rawState, &existing)
	}
	if existing.FeedbackID == feedbackID && existing.LastAction == string(action) {
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(memoryUseResponse{Status: "idempotent", MemoryID: id, Action: action, FeedbackID: feedbackID, ItemRevision: revision, Suppressed: existing.Suppressed, CurationWeight: weight})
		return
	}
	state, nextWeight, err := BuildMemoryUseState(existing, action, feedbackID)
	if err == ErrFeedbackConflict {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if nextWeight < weight {
		nextWeight = weight
	}
	before, _ := json.Marshal(existing)
	after, _ := json.Marshal(state)
	nextRevision := revision + 1
	if _, err = tx.ExecContext(r.Context(), `UPDATE memories m JOIN users u ON u.id=m.user_id SET m.item_revision=?,m.curation_weight=?,m.memory_use=?,m.updated_at=? WHERE m.id=? AND u.external_uid=? AND m.item_revision=?`, nextRevision, nextWeight, after, time.Now().UTC(), id, uid, revision); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	result, _ := json.Marshal(memoryUseResponse{Status: "ok", MemoryID: id, Action: action, FeedbackID: feedbackID, ItemRevision: nextRevision, Suppressed: state.Suppressed, CurationWeight: nextWeight})
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO memory_mutation_journal(user_external_uid,memory_id,mutation_kind,feedback_id,action,expected_revision,before_state,after_state,result,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, uid, id, "memory_use", feedbackID, action, revision, before, after, result, time.Now().UTC()); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(memoryUseResponse{Status: "ok", MemoryID: id, Action: action, FeedbackID: feedbackID, ItemRevision: nextRevision, Suppressed: state.Suppressed, CurationWeight: nextWeight})
}

func (h Handler) Revert(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.DB == nil {
		http.Error(w, "memory canonical storage is not configured", http.StatusServiceUnavailable)
		return
	}
	var in struct {
		OperationID string `json:"operation_id"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.OperationID) == "" {
		http.Error(w, "operation_id is required", http.StatusBadRequest)
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback()
	id := r.PathValue("memory_id")
	var originalID int64
	var before, after, priorResult []byte
	var mutationKind string
	err = tx.QueryRowContext(r.Context(), `SELECT journal_id,before_state,after_state,result,mutation_kind FROM memory_mutation_journal WHERE user_external_uid=? AND memory_id=? AND operation_id=? FOR UPDATE`, uid, id, in.OperationID).Scan(&originalID, &before, &after, &priorResult, &mutationKind)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var revision int64
	var weight int
	var current []byte
	if err = tx.QueryRowContext(r.Context(), `SELECT m.item_revision,m.curation_weight,m.memory_use FROM memories m JOIN users u ON u.id=m.user_id WHERE m.id=? AND u.external_uid=? FOR UPDATE`, id, uid).Scan(&revision, &weight, &current); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var already struct {
		Status string `json:"status"`
		Memory Item   `json:"memory"`
	}
	var existingRevert []byte
	if tx.QueryRowContext(r.Context(), `SELECT result FROM memory_mutation_journal WHERE user_external_uid=? AND memory_id=? AND operation_id=?`, uid, id, "revert:"+in.OperationID).Scan(&existingRevert) == nil {
		w.Header().Set("Cache-Control", "no-store")
		_ = json.Unmarshal(existingRevert, &already)
		_ = json.NewEncoder(w).Encode(already)
		return
	}
	_ = originalID
	_ = after
	_ = mutationKind
	nextRevision := revision + 1
	if _, err = tx.ExecContext(r.Context(), `UPDATE memories m JOIN users u ON u.id=m.user_id SET m.item_revision=?,m.memory_use=?,m.curation_weight=?,m.updated_at=? WHERE m.id=? AND u.external_uid=? AND m.item_revision=?`, nextRevision, before, weight, time.Now().UTC(), id, uid, revision); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	result := map[string]any{"status": "ok", "memory": map[string]any{"id": id, "item_revision": nextRevision}}
	resultJSON, _ := json.Marshal(result)
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO memory_mutation_journal(user_external_uid,memory_id,mutation_kind,operation_id,before_state,after_state,result,created_at) VALUES(?,?,?,?,?,?,?,?)`, uid, id, "memory_revert", "revert:"+in.OperationID, current, before, resultJSON, time.Now().UTC()); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = priorResult
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}
func (h Handler) Item(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	id := r.PathValue("memory_id")
	switch r.Method {
	case http.MethodGet:
		item, err := h.Service.Get(r.Context(), uid, id)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(item)
	case http.MethodPatch:
		var in UpdateInput
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		item, err := h.Service.Update(r.Context(), uid, id, in)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(item)
	case http.MethodDelete:
		if err := h.Service.Delete(r.Context(), uid, id); errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h Handler) LedgerHistory(w http.ResponseWriter, r *http.Request) {
	// The relational store is append-oriented for memory rows. Preserve the
	// Python route contract while applying the same owner filter as the main
	// collection; archived rows are intentionally included here.
	h.Collection(w, r)
}

func (h Handler) Review(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.DB == nil {
		http.Error(w, "memory review storage is not configured", http.StatusServiceUnavailable)
		return
	}
	value := r.URL.Query().Get("value")
	if value == "" {
		var in struct {
			Value *bool `json:"value"`
		}
		if json.NewDecoder(r.Body).Decode(&in) == nil && in.Value != nil {
			if *in.Value {
				value = "true"
			} else {
				value = "false"
			}
		}
	}
	if value != "true" && value != "false" {
		http.Error(w, "value must be a boolean", http.StatusBadRequest)
		return
	}
	id, e := strconv.ParseInt(r.PathValue("memory_id"), 10, 64)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	result, e := h.DB.ExecContext(r.Context(), `UPDATE memories m JOIN users u ON u.id=m.user_id SET m.user_review=?,m.updated_at=? WHERE m.id=? AND u.external_uid=?`, value == "true", time.Now().UTC(), id, uid)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		http.NotFound(w, r)
		return
	}
	_, _ = h.DB.ExecContext(r.Context(), `UPDATE memory_review_conflicts SET status='resolved',resolution=JSON_OBJECT('decision',?),resolved_at=? WHERE user_external_uid=? AND memory_id=? AND status='pending'`, value, time.Now().UTC(), uid, id)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h Handler) Baseline(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if h.DB == nil {
		http.Error(w, "memory storage is not configured", 503)
		return
	}
	value := r.URL.Query().Get("value")
	if value == "" {
		var in struct {
			Value *bool `json:"value"`
		}
		if json.NewDecoder(r.Body).Decode(&in) == nil && in.Value != nil {
			if *in.Value {
				value = "true"
			} else {
				value = "false"
			}
		}
	}
	if value != "true" && value != "false" {
		http.Error(w, "value must be a boolean", 400)
		return
	}
	id, e := strconv.ParseInt(r.PathValue("memory_id"), 10, 64)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	res, e := h.DB.ExecContext(r.Context(), `UPDATE memories m JOIN users u ON u.id=m.user_id SET m.is_baseline=?,m.updated_at=? WHERE m.id=? AND u.external_uid=?`, value == "true", time.Now().UTC(), id, uid)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		http.NotFound(w, r)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h Handler) ReviewQueue(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if h.DB == nil {
		http.Error(w, "memory review storage is not configured", 503)
		return
	}
	if r.Method == http.MethodGet && r.PathValue("review_id") == "" {
		status := r.URL.Query().Get("status")
		if status == "" {
			status = "pending"
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit < 1 || limit > 500 {
			limit = 100
		}
		rows, e := h.DB.QueryContext(r.Context(), `SELECT review_id,payload,status,resolution,created_at,resolved_at FROM memory_review_conflicts WHERE user_external_uid=? AND status=? ORDER BY created_at DESC LIMIT ?`, uid, status, limit)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, st string
			var p, rs []byte
			var created time.Time
			var resolved *time.Time
			if rows.Scan(&id, &p, &st, &rs, &created, &resolved) != nil {
				continue
			}
			var payload, resolution any
			_ = json.Unmarshal(p, &payload)
			if len(rs) > 0 {
				_ = json.Unmarshal(rs, &resolution)
			}
			out = append(out, map[string]any{"review_id": id, "status": st, "payload": payload, "resolution": resolution, "created_at": created, "resolved_at": resolved})
		}
		json.NewEncoder(w).Encode(out)
		return
	}
	id := r.PathValue("review_id")
	if id == "" {
		http.Error(w, "review_id is required", 400)
		return
	}
	var p, rs []byte
	var st string
	var created time.Time
	var resolved *time.Time
	var e error
	e = h.DB.QueryRowContext(r.Context(), `SELECT payload,status,resolution,created_at,resolved_at FROM memory_review_conflicts WHERE user_external_uid=? AND review_id=?`, uid, id).Scan(&p, &st, &rs, &created, &resolved)
	if errors.Is(e, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	var payload, resolution any
	_ = json.Unmarshal(p, &payload)
	if len(rs) > 0 {
		_ = json.Unmarshal(rs, &resolution)
	}
	if r.Method == http.MethodGet {
		json.NewEncoder(w).Encode(map[string]any{"review_id": id, "status": st, "payload": payload, "resolution": resolution, "created_at": created, "resolved_at": resolved})
		return
	}
	var in struct {
		Decision        string `json:"decision"`
		Correction      string `json:"correction"`
		Reason          string `json:"reason"`
		CurrentVeracity *bool  `json:"current_veracity"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Decision != "accept" && in.Decision != "reject" && in.Decision != "correct" && in.Decision != "timeout" {
		http.Error(w, "invalid review decision", 400)
		return
	}
	raw, _ := json.Marshal(in)
	now := time.Now().UTC()
	_, e = h.DB.ExecContext(r.Context(), `UPDATE memory_review_conflicts SET status='resolved',resolution=?,resolved_at=? WHERE user_external_uid=? AND review_id=? AND status='pending'`, raw, now, uid, id)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"review_id": id, "status": "resolved", "decision": in.Decision, "resolved_at": now})
}

func (h Handler) CreateReviewConflict(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if h.DB == nil {
		http.Error(w, "memory review storage is not configured", 503)
		return
	}
	var payload map[string]any
	if json.NewDecoder(r.Body).Decode(&payload) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	raw, _ := json.Marshal(payload)
	id := "review_" + uuid.NewString()
	now := time.Now().UTC()
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO memory_review_conflicts(review_id,user_external_uid,payload,created_at) VALUES(?,?,?,?)`, id, uid, raw, now)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"review_id": id, "status": "pending", "payload": payload, "created_at": now})
}
