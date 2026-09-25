package releases

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"strings"
	"time"
)

func previewDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func previewLanding(manifest previewManifest) string {
	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>%s</title></head><body><h1>%s</h1><p>Source: %s</p><a href="%s">Download Omi Preview</a></body></html>`, html.EscapeString(manifest.AppName), html.EscapeString(manifest.AppName), html.EscapeString(manifest.SourceSHA), html.EscapeString(manifest.DMGURL))
}

func (h Handler) previewPublishAuthorized(r *http.Request) bool {
	want := strings.TrimSpace(os.Getenv("DESKTOP_PREVIEW_PUBLISH_KEY"))
	return want != "" && strings.TrimSpace(r.Header.Get("secret-key")) == want
}

func (h Handler) PublishPreview(w http.ResponseWriter, r *http.Request) {
	if !h.previewPublishAuthorized(r) {
		writeJSONError(w, http.StatusForbidden, "You are not authorized to publish desktop previews")
		return
	}
	if h.DB == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "release storage is not configured")
		return
	}
	var manifest previewManifest
	if json.NewDecoder(r.Body).Decode(&manifest) != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid preview manifest")
		return
	}
	if err := validatePreviewManifest(manifest); err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	raw, _ := json.Marshal(manifest)
	digest := previewDigest(raw)
	var expected *int64
	if rawExpected := r.URL.Query().Get("expected_generation"); rawExpected != "" {
		var value int64
		if _, err := fmt.Sscan(rawExpected, &value); err != nil || value < 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid expected_generation")
			return
		}
		expected = &value
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "release storage is not configured")
		return
	}
	var existingDigest string
	err = tx.QueryRowContext(r.Context(), `SELECT manifest_sha256 FROM desktop_preview_manifests WHERE slug=? AND source_sha=? FOR UPDATE`, manifest.Slug, strings.ToLower(manifest.SourceSHA)).Scan(&existingDigest)
	if err == nil && existingDigest != digest {
		_ = tx.Rollback()
		writeJSONError(w, http.StatusConflict, "preview artifact already exists with different immutable metadata")
		return
	}
	if err != nil && err != sql.ErrNoRows {
		_ = tx.Rollback()
		writeJSONError(w, http.StatusInternalServerError, "failed to read preview manifest")
		return
	}
	if err == sql.ErrNoRows {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO desktop_preview_manifests(slug,source_sha,manifest,manifest_sha256,created_at) VALUES(?,?,?,?,?)`, manifest.Slug, strings.ToLower(manifest.SourceSHA), raw, digest, time.Now().UTC())
	}
	var currentSource string
	var generation int64
	pointerErr := tx.QueryRowContext(r.Context(), `SELECT source_sha,generation FROM desktop_preview_pointers WHERE slug=? FOR UPDATE`, manifest.Slug).Scan(&currentSource, &generation)
	if pointerErr == sql.ErrNoRows {
		pointerErr = nil
	}
	if pointerErr == nil && expected != nil && *expected != generation {
		pointerErr = fmt.Errorf("generation mismatch: expected %d, current %d", *expected, generation)
	}
	newGeneration := generation
	if pointerErr == nil && currentSource != strings.ToLower(manifest.SourceSHA) {
		newGeneration++
		_, pointerErr = tx.ExecContext(r.Context(), `INSERT INTO desktop_preview_pointers(slug,source_sha,generation,updated_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE source_sha=VALUES(source_sha),generation=VALUES(generation),updated_at=VALUES(updated_at)`, manifest.Slug, strings.ToLower(manifest.SourceSHA), newGeneration, time.Now().UTC())
	}
	if err == nil {
		err = pointerErr
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "manifest": manifest, "generation": newGeneration, "idempotent": currentSource == strings.ToLower(manifest.SourceSHA)})
}

func (h Handler) Preview(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	source := r.PathValue("source_sha")
	var raw []byte
	var err error
	if source == "" {
		err = h.DB.QueryRowContext(r.Context(), `SELECT m.manifest FROM desktop_preview_pointers p JOIN desktop_preview_manifests m ON m.slug=p.slug AND m.source_sha=p.source_sha WHERE p.slug=?`, slug).Scan(&raw)
	} else {
		err = h.DB.QueryRowContext(r.Context(), `SELECT manifest FROM desktop_preview_manifests WHERE slug=? AND source_sha=?`, slug, strings.ToLower(source)).Scan(&raw)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var manifest previewManifest
	if json.Unmarshal(raw, &manifest) != nil {
		http.Error(w, "stored preview manifest is invalid", 500)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(previewLanding(manifest)))
}

func (h Handler) DelistPreview(w http.ResponseWriter, r *http.Request) {
	if !h.previewPublishAuthorized(r) {
		writeJSONError(w, http.StatusForbidden, "You are not authorized to delist desktop previews")
		return
	}
	slug := r.PathValue("slug")
	var input struct {
		ExpectedGeneration int64 `json:"expected_generation"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || input.ExpectedGeneration < 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid expected_generation")
		return
	}
	result, err := h.DB.ExecContext(r.Context(), `DELETE FROM desktop_preview_pointers WHERE slug=? AND generation=?`, slug, input.ExpectedGeneration)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "failed to delist preview")
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "slug": slug, "deleted": false, "generation": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "slug": slug, "deleted": true, "generation": input.ExpectedGeneration})
}
