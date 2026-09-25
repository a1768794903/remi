package memories

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"remi/server/internal/auth"
	"strings"
	"time"
)

type memoryImportItem struct {
	ExternalID     string         `json:"external_id"`
	OccurredAt     *time.Time     `json:"occurred_at"`
	Title          string         `json:"title"`
	Snippet        string         `json:"snippet"`
	Content        string         `json:"content"`
	ContentHash    string         `json:"content_hash"`
	Metadata       map[string]any `json:"metadata"`
	ClientDeviceID string         `json:"client_device_id"`
}

type memoryImportRequest struct {
	SourceType        string             `json:"source_type"`
	ImportRunID       string             `json:"import_run_id"`
	SourceAccountHash string             `json:"source_account_hash"`
	ImporterVersion   string             `json:"importer_version"`
	ExtractorVersion  string             `json:"extractor_version"`
	Items             []memoryImportItem `json:"items"`
}

func normalizeImportSource(value string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "-", "_")), "_")
}

func stableImportContentHash(item memoryImportItem) string {
	if strings.TrimSpace(item.ContentHash) != "" {
		return strings.TrimSpace(item.ContentHash)
	}
	h := sha256.New()
	for _, value := range []string{item.Title, item.Snippet, item.Content} {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func stableImportID(uid, source, account, externalID, contentHash string) string {
	h := sha256.Sum256([]byte("memory-import-artifact|" + uid + "|" + source + "|" + account + "|" + externalID + "|" + contentHash))
	return "mia_" + hex.EncodeToString(h[:])
}

func stableImportRunID(uid string, in memoryImportRequest, source string) string {
	if strings.TrimSpace(in.ImportRunID) != "" {
		return strings.TrimSpace(in.ImportRunID)
	}
	h := sha256.Sum256([]byte("memory-import-run|" + uid + "|" + source + "|" + in.SourceAccountHash + "|" + in.ImporterVersion))
	return "mir_" + hex.EncodeToString(h[:])
}

func validateImportItem(item memoryImportItem) error {
	if strings.TrimSpace(item.ExternalID) == "" && strings.TrimSpace(item.ContentHash) == "" && strings.TrimSpace(item.Title+item.Snippet+item.Content) == "" {
		return errors.New("import artifact requires external_id, content_hash, or textual content")
	}
	return nil
}

func (h Handler) ImportBatch(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if h.DB == nil {
		http.Error(w, "memory import storage is not configured", http.StatusServiceUnavailable)
		return
	}
	var in memoryImportRequest
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.SourceType) == "" {
		http.Error(w, "source_type is required", http.StatusBadRequest)
		return
	}
	if len(in.Items) > 100 {
		http.Error(w, "items exceeds 100", http.StatusRequestEntityTooLarge)
		return
	}
	if in.ImporterVersion == "" {
		in.ImporterVersion = "v1"
	}
	source := normalizeImportSource(in.SourceType)
	for _, item := range in.Items {
		if err := validateImportItem(item); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	runID := stableImportRunID(uid, in, source)
	now := time.Now().UTC()
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback()
	var existingOwner string
	ownerErr := tx.QueryRowContext(r.Context(), `SELECT user_external_uid FROM memory_import_runs WHERE run_id=? FOR UPDATE`, runID).Scan(&existingOwner)
	if ownerErr == nil && existingOwner != uid {
		http.Error(w, "import_run_id belongs to another user", http.StatusConflict)
		return
	}
	if ownerErr != nil && ownerErr != sql.ErrNoRows {
		http.Error(w, ownerErr.Error(), http.StatusInternalServerError)
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO memory_import_runs(run_id,user_external_uid,source_type,source_account_hash,importer_version,extractor_version,status,started_at,updated_at) VALUES(?,?,?,?,?,?,?, ?,?) ON DUPLICATE KEY UPDATE updated_at=VALUES(updated_at)`, runID, uid, source, nullString(in.SourceAccountHash), in.ImporterVersion, nullString(in.ExtractorVersion), "received", now, now)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	created, deduped := 0, 0
	for _, item := range in.Items {
		hash := stableImportContentHash(item)
		artifactID := stableImportID(uid, source, in.SourceAccountHash, item.ExternalID, hash)
		metadata, _ := json.Marshal(item.Metadata)
		if len(metadata) == 0 || string(metadata) == "null" {
			metadata = []byte(`{}`)
		}
		body := any(nil)
		redaction := "title_snippet_only"
		if strings.EqualFold(strings.TrimSpace(os.Getenv("MEMORY_IMPORT_BODY_STORAGE_MODE")), "full") {
			body = nullString(item.Content)
			redaction = "importer_full_excerpt"
		}
		res, e := tx.ExecContext(r.Context(), `INSERT INTO memory_import_artifacts(artifact_id,user_external_uid,run_id,source_type,external_id,content_hash,title,snippet,redacted_body,metadata,occurred_at,captured_at,client_device_id,source_state,redaction_status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE run_id=VALUES(run_id),source_state='active',updated_at=VALUES(updated_at)`, artifactID, uid, runID, source, nullString(item.ExternalID), hash, nullString(item.Title), nullString(item.Snippet), body, metadata, item.OccurredAt, now, nullString(item.ClientDeviceID), "active", redaction, now, now)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		affected, _ := res.RowsAffected()
		if affected == 1 {
			created++
		} else {
			deduped++
		}
	}
	_, err = tx.ExecContext(r.Context(), `UPDATE memory_import_runs SET artifact_count=artifact_count+?,deduped_count=deduped_count+?,updated_at=? WHERE run_id=? AND user_external_uid=?`, created, deduped, now, runID, uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"run_id": runID, "artifacts_received": len(in.Items), "artifacts_created": created, "artifacts_deduped": deduped, "candidates_created": 0, "status": "received"})
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
