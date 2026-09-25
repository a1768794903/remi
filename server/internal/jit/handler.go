package jit

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"remi/server/internal/auth"
)

type Decision struct {
	Rollout               string `json:"rollout"`
	KillSwitch            string `json:"kill_switch"`
	Effective             string `json:"effective"`
	Reason                string `json:"reason"`
	ErrorClass            string `json:"error_class"`
	CacheHit              bool   `json:"cache_hit"`
	CacheTTLSeconds       int    `json:"cache_ttl_seconds"`
	BudgetContractVersion string `json:"budget_contract_version,omitempty"`
}

type TriggerSnapshot struct {
	OwnerID           string `json:"owner_id"`
	AccountGeneration int64  `json:"account_generation"`
	HeadCommitID      string `json:"head_commit_id"`
	CommitSequence    int64  `json:"commit_sequence"`
	SnapshotRevision  string `json:"snapshot_revision"`
	Complete          bool   `json:"complete"`
	Rows              []any  `json:"rows"`
	FailureReason     string `json:"failure_reason,omitempty"`
}

type PromptSnapshot struct {
	SchemaVersion      string `json:"schema_version"`
	Mode               string `json:"mode"`
	Reason             string `json:"reason"`
	SourceHeadCommitID string `json:"source_head_commit_id,omitempty"`
	Rows               []any  `json:"rows"`
}

type MirrorSnapshot struct {
	SchemaVersion     string `json:"schema_version"`
	OwnerID           string `json:"owner_id"`
	AccountGeneration int64  `json:"account_generation"`
	SourceGeneration  int64  `json:"source_generation"`
	WriterEpoch       int64  `json:"writer_epoch"`
	HeadCommitID      string `json:"head_commit_id"`
	CommitSequence    int64  `json:"commit_sequence"`
	EpochID           string `json:"epoch_id"`
	PageRevision      string `json:"page_revision"`
	ChainRevision     string `json:"chain_revision"`
	ScannedCount      int    `json:"scanned_count"`
	ProjectedCount    int    `json:"projected_count"`
	Rows              []any  `json:"rows"`
	Aliases           []any  `json:"aliases"`
	FinalPage         bool   `json:"final_page"`
	FailureReason     string `json:"failure_reason,omitempty"`
}

type Handler struct{}

func resolveDecision(uid string) Decision {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("JIT_KILL_SWITCH_ENABLED")), "true") {
		return Decision{Rollout: "enabled", KillSwitch: "enabled", Effective: "disabled", Reason: "kill_switch_enabled", ErrorClass: "none", CacheTTLSeconds: 0}
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("JIT_ROLLOUT_ENABLED")), "true") {
		return Decision{Rollout: "enabled", KillSwitch: "disabled", Effective: "enabled", Reason: "rollout_enabled", ErrorClass: "none", CacheTTLSeconds: 0}
	}
	_ = uid
	return Decision{Rollout: "disabled", KillSwitch: "disabled", Effective: "disabled", Reason: "configuration_missing", ErrorClass: "configuration", CacheTTLSeconds: 0}
}

func uid(r *http.Request) (string, error) { return auth.UserID(r.Context()) }

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h Handler) RolloutDecision(w http.ResponseWriter, r *http.Request) {
	u, err := uid(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	decision := resolveDecision(u)
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OMI_JIT_PROACTIVITY_BUDGET_CONTRACT")), "jit-cloud-qa-v1") {
		decision.BudgetContractVersion = "jit-cloud-qa-v1"
	}
	writeJSON(w, http.StatusOK, decision)
}

func (h Handler) TriggerSnapshot(w http.ResponseWriter, r *http.Request) {
	u, err := uid(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	decision := resolveDecision(u)
	if decision.Effective != "enabled" {
		writeJSON(w, http.StatusOK, TriggerSnapshot{OwnerID: u, Rows: []any{}, FailureReason: "rollout_not_enabled"})
		return
	}
	writeJSON(w, http.StatusOK, TriggerSnapshot{OwnerID: u, Rows: []any{}, FailureReason: "trigger_authority_unavailable"})
}

func (h Handler) PromptSnapshot(w http.ResponseWriter, r *http.Request) {
	u, err := uid(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	decision := resolveDecision(u)
	mode := "unknown"
	reason := decision.Reason
	if decision.KillSwitch == "enabled" {
		mode = "killed"
	} else if decision.Effective == "disabled" {
		mode = "disabled"
	}
	writeJSON(w, http.StatusOK, PromptSnapshot{SchemaVersion: "knowledge-ledger-v1", Mode: mode, Reason: reason, Rows: []any{}})
}

func (h Handler) MirrorSnapshot(w http.ResponseWriter, r *http.Request) {
	u, err := uid(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, MirrorSnapshot{SchemaVersion: "knowledge-ledger-mirror-v1", OwnerID: u, Rows: []any{}, Aliases: []any{}, FailureReason: "rollout_not_enabled"})
}

func (h Handler) TriggerFeedback(w http.ResponseWriter, r *http.Request) {
	if _, err := uid(r); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	// Feedback is accepted only by the authoritative Firestore adapter. Until
	// that adapter is available in Go, fail closed instead of acknowledging a
	// mutation that cannot be durably applied.
	http.Error(w, "Trigger feedback authority changed or is unavailable", http.StatusConflict)
}

func (h Handler) Reservation(w http.ResponseWriter, r *http.Request) {
	if _, err := uid(r); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	http.Error(w, "JIT proactive work is disabled", http.StatusForbidden)
}
