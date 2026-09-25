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
	if _, err := auth.UserID(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"intents": []any{}, "receipt_outcomes": []any{}, "rejection_outcomes": []any{}})
}

func (h Handler) Deferral(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.UserID(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if !chatFirstEnabled() {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	http.Error(w, "chat-first deferral storage is not configured", http.StatusServiceUnavailable)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
