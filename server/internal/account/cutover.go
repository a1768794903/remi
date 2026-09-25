package account

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"remi/server/internal/auth"
)

type CutoverRecord struct {
	UID                     string
	State                   string
	AccountGeneration       int
	UIGeneration            int
	APIGeneration           int
	StrandedNewData         bool
	OfflineQueueInstruction string
	CheckpointPhase         string
	CheckpointToken         string
	ManifestID              string
	DestinationBackendBound bool
}

type CutoverManifest struct {
	ManifestID              string `json:"manifest_id,omitempty"`
	CheckpointPhase         string `json:"checkpoint_phase"`
	CheckpointToken         string `json:"checkpoint_token,omitempty"`
	DestinationBackendBound bool   `json:"destination_backend_bound"`
	StrandedNewData         bool   `json:"stranded_new_data"`
}

type CutoverControl struct {
	SchemaVersion           int             `json:"schema_version"`
	State                   string          `json:"state"`
	AccountGeneration       int             `json:"account_generation"`
	UIGeneration            int             `json:"ui_generation"`
	APIGeneration           int             `json:"api_generation"`
	ClientAction            string          `json:"client_action"`
	OfflineQueueInstruction string          `json:"offline_queue_instruction"`
	StrandedNewData         bool            `json:"stranded_new_data"`
	LegacyWritesAllowed     bool            `json:"legacy_writes_allowed"`
	ProductTrafficAllowed   bool            `json:"product_traffic_allowed"`
	AuthBootstrapReachable  bool            `json:"auth_bootstrap_reachable"`
	MinimumSupportedBuilds  []PlatformBuild `json:"minimum_supported_builds"`
	Migration               CutoverManifest `json:"migration"`
}

type PlatformBuild struct {
	Platform              string `json:"platform"`
	MinimumSupportedBuild int    `json:"minimum_supported_build"`
}

func parseClientBuild(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if i := strings.LastIndex(raw, "+"); i >= 0 {
		raw = raw[i+1:]
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value >= 0
}

func BuildCutoverControl(record CutoverRecord, platform, build string) CutoverControl {
	if record.State == "" {
		record.State = "legacy"
	}
	if record.OfflineQueueInstruction == "" {
		record.OfflineQueueInstruction = "none"
	}
	if record.CheckpointPhase == "" {
		record.CheckpointPhase = "not_started"
	}
	// Legacy clients continue to receive the zero generations until an
	// operator-coordinated cutover changes the persisted record.
	action := "none"
	_, _ = parseClientBuild(build)
	_ = platform
	// The production floors are intentionally zero until a platform release
	// publishes one; malformed or absent builds remain compatible.
	if record.State == "migrating" || record.State == "new" {
		action = "migration_maintenance"
	}
	if record.State == "migrating" || record.State == "new" || record.State == "rolled_back_stranded" {
		record.OfflineQueueInstruction = "quarantine"
	}
	traffic := action == "none" && record.State != "migrating" && record.State != "new"
	return CutoverControl{
		SchemaVersion: 1, State: record.State, AccountGeneration: record.AccountGeneration,
		UIGeneration: record.UIGeneration, APIGeneration: record.APIGeneration,
		ClientAction: action, OfflineQueueInstruction: record.OfflineQueueInstruction,
		StrandedNewData: record.StrandedNewData, LegacyWritesAllowed: traffic && record.State != "rolled_back_stranded",
		ProductTrafficAllowed: traffic, AuthBootstrapReachable: true,
		MinimumSupportedBuilds: []PlatformBuild{{Platform: "android", MinimumSupportedBuild: 0}, {Platform: "ios", MinimumSupportedBuild: 0}},
		Migration:              CutoverManifest{ManifestID: record.ManifestID, CheckpointPhase: record.CheckpointPhase, CheckpointToken: record.CheckpointToken, DestinationBackendBound: record.DestinationBackendBound, StrandedNewData: record.StrandedNewData},
	}
}

func readCutoverRecord(db *sql.DB, uid string) (CutoverRecord, error) {
	var record CutoverRecord
	err := db.QueryRow(`SELECT state, account_generation, ui_generation, api_generation, stranded_new_data, offline_queue_instruction, checkpoint_phase, checkpoint_token, manifest_id, destination_backend_bound FROM account_cutover WHERE uid=?`, uid).Scan(
		&record.State, &record.AccountGeneration, &record.UIGeneration, &record.APIGeneration, &record.StrandedNewData,
		&record.OfflineQueueInstruction, &record.CheckpointPhase, &record.CheckpointToken, &record.ManifestID, &record.DestinationBackendBound,
	)
	if err == sql.ErrNoRows {
		return CutoverRecord{UID: uid, State: "legacy"}, nil
	}
	record.UID = uid
	return record, err
}

func (h Handler) CutoverControl(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.Wipe.DB == nil {
		http.Error(w, "account cutover storage is not configured", http.StatusServiceUnavailable)
		return
	}
	record, err := readCutoverRecord(h.Wipe.DB, uid)
	if err != nil {
		http.Error(w, "account cutover state unavailable", http.StatusServiceUnavailable)
		return
	}
	build := r.Header.Get("X-App-Build")
	if build == "" {
		build = r.Header.Get("X-App-Version")
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(BuildCutoverControl(record, r.Header.Get("X-App-Platform"), build))
}
