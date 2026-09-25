package chatfirst

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/auth"
)

type Handler struct{ DB *sql.DB }

type block struct {
	Type string `json:"type"`
	ID   string `json:"task_id,omitempty"`
}
type validationRequest struct {
	SourceSurface     string  `json:"source_surface"`
	ControlGeneration int64   `json:"control_generation"`
	OwnerFence        string  `json:"owner_fence"`
	Blocks            []block `json:"blocks"`
}
type validationResult struct {
	Accepted bool             `json:"accepted"`
	Code     string           `json:"code"`
	Blocks   []map[string]any `json:"blocks,omitempty"`
}

type materializeReceipt struct {
	IntentID  string `json:"intent_id"`
	ReceiptID string `json:"receipt_id"`
}
type materializeRejection struct {
	IntentID string `json:"intent_id"`
	Code     string `json:"code"`
}
type materializeDeferral struct {
	IntentID string `json:"intent_id"`
	Code     string `json:"code"`
}
type materializeRequest struct {
	SourceSurface     string                 `json:"source_surface"`
	ControlGeneration int64                  `json:"control_generation"`
	OwnerFence        string                 `json:"owner_fence"`
	WindowForeground  bool                   `json:"window_foreground"`
	InitialPageLoaded bool                   `json:"initial_page_loaded"`
	Receipts          []materializeReceipt   `json:"receipts"`
	Rejections        []materializeRejection `json:"rejections"`
	Deferrals         []materializeDeferral  `json:"deferrals"`
}

func stableBlockID(uid string, generation int64, raw []byte) string {
	sum := sha256.Sum256([]byte(uid + ":" + strconv.FormatInt(generation, 10) + ":" + string(raw)))
	return "cfb_" + hex.EncodeToString(sum[:])[:24]
}

func chatFirstEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("CHAT_FIRST_ENABLED")), "true")
}

func validateRequest(uid string, request validationRequest) validationResult {
	if request.OwnerFence != uid || request.SourceSurface != "main_chat" || len(request.Blocks) == 0 || len(request.Blocks) > 8 {
		return validationResult{Code: "capability_unavailable"}
	}
	if !chatFirstEnabled() {
		return validationResult{Code: "capability_unavailable"}
	}
	seen := map[string]bool{}
	out := make([]map[string]any, 0, len(request.Blocks))
	for _, item := range request.Blocks {
		if item.Type == "" || item.ID == "" || (item.Type != "taskCard" && item.Type != "goalLink" && item.Type != "captureLink" && item.Type != "conversationLink" && item.Type != "memoryLink") {
			return validationResult{Code: "invalid_request"}
		}
		raw, _ := json.Marshal(item)
		id := stableBlockID(uid, request.ControlGeneration, raw)
		if seen[id] {
			return validationResult{Code: "invalid_request"}
		}
		seen[id] = true
		out = append(out, map[string]any{"id": id, "type": item.Type, "id_value": item.ID})
	}
	return validationResult{Accepted: true, Code: "accepted", Blocks: out}
}

func (h Handler) Validate(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var in validationRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil {
		writeJSON(w, http.StatusOK, validationResult{Code: "invalid_request"})
		return
	}
	result := validateRequest(uid, in)
	writeJSON(w, http.StatusOK, result)
}

func (h Handler) Materialize(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var in materializeRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in) != nil {
		http.Error(w, "invalid request", http.StatusUnprocessableEntity)
		return
	}
	if in.OwnerFence != uid || in.SourceSurface != "main_chat" || !chatFirstEnabled() {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	result := map[string]any{"intents": []any{}, "receipt_outcomes": []any{}, "rejection_outcomes": []any{}}
	if !in.WindowForeground || !in.InitialPageLoaded {
		writeJSON(w, http.StatusOK, result)
		return
	}
	if h.DB == nil {
		http.Error(w, "chat-first intent storage is not configured", http.StatusServiceUnavailable)
		return
	}
	receiptOutcomes := make([]map[string]string, 0, len(in.Receipts))
	for _, receipt := range in.Receipts {
		res, e := h.DB.ExecContext(r.Context(), `UPDATE chat_first_intents SET delivery_state='delivered',delivered_at=UTC_TIMESTAMP(6),materialization_attempts=materialization_attempts+1 WHERE intent_id=? AND user_external_uid=? AND account_generation=? AND delivery_state IN ('ready','pending_kernel_receipt')`, receipt.IntentID, uid, in.ControlGeneration)
		if e != nil {
			http.Error(w, "chat-first intent storage unavailable", 503)
			return
		}
		n, _ := res.RowsAffected()
		outcome := "missing"
		if n > 0 {
			outcome = "acknowledged"
		}
		receiptOutcomes = append(receiptOutcomes, map[string]string{"intent_id": receipt.IntentID, "outcome": outcome})
	}
	rejectionOutcomes := make([]map[string]string, 0, len(in.Rejections))
	for _, rejection := range in.Rejections {
		res, e := h.DB.ExecContext(r.Context(), `UPDATE chat_first_intents SET delivery_state='dead_letter',last_rejection_code=?,last_rejection_at=UTC_TIMESTAMP(6),materialization_attempts=materialization_attempts+1 WHERE intent_id=? AND user_external_uid=? AND account_generation=? AND delivery_state IN ('ready','pending_kernel_receipt')`, rejection.Code, rejection.IntentID, uid, in.ControlGeneration)
		if e != nil {
			http.Error(w, "chat-first intent storage unavailable", 503)
			return
		}
		n, _ := res.RowsAffected()
		outcome := "missing"
		if n > 0 {
			outcome = "recorded"
		}
		rejectionOutcomes = append(rejectionOutcomes, map[string]string{"intent_id": rejection.IntentID, "outcome": outcome})
	}
	rows, e := h.DB.QueryContext(r.Context(), `SELECT payload FROM chat_first_intents WHERE user_external_uid=? AND account_generation=? AND delivery_state='ready' ORDER BY created_at ASC LIMIT 16`, uid, in.ControlGeneration)
	if e != nil {
		http.Error(w, "chat-first intent storage unavailable", 503)
		return
	}
	intents := make([]any, 0)
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			http.Error(w, "chat-first intent storage unavailable", 503)
			return
		}
		var value any
		if json.Unmarshal(raw, &value) == nil {
			intents = append(intents, value)
		}
	}
	if e = rows.Err(); e != nil {
		http.Error(w, "chat-first intent storage unavailable", 503)
		return
	}
	result["intents"], result["receipt_outcomes"], result["rejection_outcomes"] = intents, receiptOutcomes, rejectionOutcomes
	writeJSON(w, http.StatusOK, result)
}

func (h Handler) Deferral(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if !chatFirstEnabled() {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if h.DB == nil {
		http.Error(w, "chat-first deferral storage is not configured", 503)
		return
	}
	var in struct {
		SourceSurface     string         `json:"source_surface"`
		ControlGeneration int64          `json:"control_generation"`
		OwnerFence        string         `json:"owner_fence"`
		ContinuityKey     string         `json:"continuity_key"`
		Subject           map[string]any `json:"subject"`
		Question          map[string]any `json:"question"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) != nil || in.SourceSurface != "main_chat" || in.OwnerFence != uid || in.ContinuityKey == "" {
		http.Error(w, "invalid request", 422)
		return
	}
	subject, _ := json.Marshal(in.Subject)
	question, _ := json.Marshal(in.Question)
	hash := sha256.Sum256([]byte(uid + ":" + strconv.FormatInt(in.ControlGeneration, 10) + ":" + in.ContinuityKey))
	id := "def_" + hex.EncodeToString(hash[:])[:32]
	now := time.Now().UTC()
	due := now.Add(24 * time.Hour)
	_, err = h.DB.ExecContext(r.Context(), `INSERT INTO chat_first_deferrals(deferral_id,user_external_uid,continuity_key,account_generation,subject,question,created_at,due_at,state) VALUES(?,?,?,?,?,?,?,?,'pending') ON DUPLICATE KEY UPDATE deferral_id=deferral_id`, id, uid, in.ContinuityKey, in.ControlGeneration, subject, question, now, due)
	if err != nil {
		http.Error(w, "chat-first deferral storage unavailable", 503)
		return
	}
	writeJSON(w, 200, map[string]any{"deferral_id": id, "due_at": due, "state": "pending"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
