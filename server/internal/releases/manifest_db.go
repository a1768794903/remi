package releases

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (h Handler) RegisterManifest(w http.ResponseWriter, r *http.Request) {
	if !h.adminAuthorized(r) {
		writeJSONError(w, http.StatusForbidden, "You are not authorized to perform this action")
		return
	}
	var manifest releaseManifest
	if json.NewDecoder(r.Body).Decode(&manifest) != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid release manifest")
		return
	}
	if h.DB == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "release storage is not configured")
		return
	}
	raw, digest, err := canonicalManifestJSON(manifest)
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "release storage is not configured")
		return
	}
	var existingDigest string
	err = tx.QueryRowContext(r.Context(), `SELECT manifest_sha256 FROM desktop_release_manifests WHERE release_id=? FOR UPDATE`, manifest.ReleaseID).Scan(&existingDigest)
	if err == nil {
		_ = tx.Rollback()
		if existingDigest != digest {
			writeJSONError(w, http.StatusConflict, "release_id already exists with different immutable metadata")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "manifest": manifest, "idempotent": true})
		return
	}
	if err != sql.ErrNoRows {
		_ = tx.Rollback()
		writeJSONError(w, http.StatusInternalServerError, "failed to read release manifest")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO desktop_release_manifests(release_id,platform,channel,version,build_number,manifest,manifest_sha256,created_at) VALUES(?,?,?,?,?,?,?,?)`, manifest.ReleaseID, manifest.Platform, manifest.Channel, manifest.Version, manifest.BuildNumber, raw, digest, time.Now().UTC())
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		writeJSONError(w, http.StatusConflict, "failed to register release manifest")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "manifest": manifest, "idempotent": false})
}

func (h Handler) ManifestV2(w http.ResponseWriter, r *http.Request) {
	if !h.adminAuthorized(r) {
		writeJSONError(w, http.StatusForbidden, "You are not authorized to perform this action")
		return
	}
	if h.DB == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "release storage is not configured")
		return
	}
	id := strings.TrimSpace(r.PathValue("release_id"))
	var raw []byte
	var digest string
	err := h.DB.QueryRowContext(r.Context(), `SELECT manifest,manifest_sha256 FROM desktop_release_manifests WHERE release_id=?`, id).Scan(&raw, &digest)
	if err == sql.ErrNoRows {
		writeJSONError(w, http.StatusNotFound, "desktop release manifest not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to read desktop release manifest")
		return
	}
	var manifest releaseManifest
	if json.Unmarshal(raw, &manifest) != nil {
		writeJSONError(w, http.StatusInternalServerError, "stored release manifest is invalid")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "manifest": manifest, "manifest_sha256": digest})
}

func (h Handler) PromoteBetaCandidate(w http.ResponseWriter, r *http.Request) {
	if !h.betaTokenAuthorized(r) {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var input struct {
		Tag string `json:"tag"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid candidate")
		return
	}
	tag := strings.TrimSpace(input.Tag)
	if _, err := parseBetaCandidateTag(tag); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "release storage is not configured")
		return
	}
	control, err := readBetaControl(tx)
	if err == nil {
		control, err = captureBetaAdmission(control, tag)
	}
	var manifest releaseManifest
	var raw []byte
	if err == nil {
		err = tx.QueryRowContext(r.Context(), `SELECT manifest FROM desktop_release_manifests WHERE release_id=? FOR UPDATE`, tag).Scan(&raw)
	}
	if err == nil {
		err = json.Unmarshal(raw, &manifest)
	}
	if err == nil && manifest.ReleaseID != tag {
		err = sql.ErrNoRows
	}
	var current channelPointer
	if err == nil {
		var currentPlatform, currentChannel, currentRelease, currentVersion string
		var currentBuild int
		err = tx.QueryRowContext(r.Context(), `SELECT platform,channel,release_id,version,build_number,generation FROM desktop_channel_pointers WHERE platform='macos' AND channel='beta' FOR UPDATE`).Scan(&currentPlatform, &currentChannel, &currentRelease, &currentVersion, &currentBuild, &current.Generation)
		if err == sql.ErrNoRows {
			err = nil
		} else {
			current = channelPointer{Platform: currentPlatform, Channel: currentChannel, ReleaseID: currentRelease, Version: currentVersion, Build: currentBuild, Generation: current.Generation}
		}
	}
	var pointer channelPointer
	if err == nil {
		pointer, err = buildBetaPointer(current, manifest, nil)
	}
	if err == nil && pointer.ReleaseID != current.ReleaseID {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO desktop_channel_pointers(platform,channel,release_id,version,build_number,generation,updated_at) VALUES(?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE release_id=VALUES(release_id),version=VALUES(version),build_number=VALUES(build_number),generation=VALUES(generation),updated_at=VALUES(updated_at)`, pointer.Platform, pointer.Channel, pointer.ReleaseID, pointer.Version, pointer.Build, pointer.Generation, time.Now().UTC())
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		writeJSONError(w, http.StatusConflict, "beta candidate promotion conflict")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tag": tag, "release_id": tag, "generation": pointer.Generation, "idempotent": pointer.ReleaseID == current.ReleaseID})
}

func (h Handler) BetaBreakglass(w http.ResponseWriter, r *http.Request) {
	if !h.adminAuthorized(r) {
		writeJSONError(w, http.StatusForbidden, "You are not authorized to perform this action")
		return
	}
	var input struct {
		Operation             string `json:"operation"`
		CurrentReleaseID      string `json:"current_release_id"`
		TargetReleaseID       string `json:"target_release_id"`
		ExpectedGeneration    int64  `json:"expected_generation"`
		NormalPathUnavailable string `json:"normal_path_unavailable"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || (input.Operation != "rollback" && input.Operation != "rollout") || strings.TrimSpace(input.CurrentReleaseID) == "" || strings.TrimSpace(input.TargetReleaseID) == "" || input.ExpectedGeneration < 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid breakglass request")
		return
	}
	if input.Operation == "rollout" && strings.TrimSpace(input.NormalPathUnavailable) == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "normal_path_unavailable is required for rollout")
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "release storage is not configured")
		return
	}
	var current channelPointer
	err = tx.QueryRowContext(r.Context(), `SELECT platform,channel,release_id,version,build_number,generation FROM desktop_channel_pointers WHERE platform='macos' AND channel='beta' FOR UPDATE`).Scan(&current.Platform, &current.Channel, &current.ReleaseID, &current.Version, &current.Build, &current.Generation)
	var raw []byte
	var manifest releaseManifest
	if err == nil {
		err = tx.QueryRowContext(r.Context(), `SELECT manifest FROM desktop_release_manifests WHERE release_id=? FOR UPDATE`, input.TargetReleaseID).Scan(&raw)
	}
	if err == nil {
		err = json.Unmarshal(raw, &manifest)
	}
	var pointer channelPointer
	if err == nil {
		pointer, err = buildBreakglassPointer(current, manifest, input.ExpectedGeneration, input.CurrentReleaseID)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE desktop_channel_pointers SET release_id=?,version=?,build_number=?,generation=?,updated_at=? WHERE platform='macos' AND channel='beta' AND generation=? AND release_id=?`, pointer.ReleaseID, pointer.Version, pointer.Build, pointer.Generation, time.Now().UTC(), input.ExpectedGeneration, input.CurrentReleaseID)
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		writeJSONError(w, http.StatusConflict, "beta breakglass conflict")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operation": input.Operation, "release_id": pointer.ReleaseID, "generation": pointer.Generation})
}
