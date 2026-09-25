package screenframes

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"remi/server/internal/auth"
)

type Handler struct{}

type Request struct {
	SchemaVersion int         `json:"schema_version"`
	AttemptID     uuid.UUID   `json:"attempt_id"`
	Purpose       string      `json:"purpose"`
	Subject       Subject     `json:"subject"`
	Candidates    []Candidate `json:"candidates"`
}
type Subject struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type Candidate struct {
	ClientFrameID  string `json:"client_frame_id"`
	CapturedAt     string `json:"captured_at"`
	MimeType       string `json:"mime_type"`
	DeclaredWidth  int    `json:"declared_width"`
	DeclaredHeight int    `json:"declared_height"`
	SHA256Base64   string `json:"sha256_base64"`
	BytesBase64    string `json:"bytes_base64"`
}

func (Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.UserID(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in Request
	decoder := json.NewDecoder(io.LimitReader(r.Body, 32<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		writeCode(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := Validate(in); err != nil {
		writeCode(w, http.StatusBadRequest, err.Error())
		return
	}
	if os.Getenv("SCREEN_FRAME_EGRESS_ENABLED") != "true" {
		writeCode(w, http.StatusConflict, "screen_frame_egress_unavailable")
		return
	}
	// Until a configured, server-side visual judge is present, never store or
	// publish a candidate. This is deliberately fail-closed: client bytes and
	// client verdicts must not become conversation evidence by default.
	if strings.TrimSpace(os.Getenv("SCREEN_FRAME_JUDGE_ENDPOINT")) == "" {
		writeCode(w, http.StatusServiceUnavailable, "judge_unavailable")
		return
	}
	writeCode(w, http.StatusNotImplemented, "screen_frame_judge_not_implemented")
}

func Validate(in Request) error {
	if in.SchemaVersion != 1 {
		return fmt.Errorf("schema_version_invalid")
	}
	if in.AttemptID == uuid.Nil {
		return fmt.Errorf("attempt_id_invalid")
	}
	if in.Purpose != "meeting_note_v1" {
		return fmt.Errorf("unknown_purpose")
	}
	if in.Subject.Kind != "conversation" || strings.TrimSpace(in.Subject.ID) == "" || len(in.Subject.ID) > 256 {
		return fmt.Errorf("unsupported_subject")
	}
	if len(in.Candidates) < 1 || len(in.Candidates) > 8 {
		return fmt.Errorf("candidate_count_invalid")
	}
	for _, candidate := range in.Candidates {
		if strings.TrimSpace(candidate.ClientFrameID) == "" || len(candidate.ClientFrameID) > 128 {
			return fmt.Errorf("client_frame_id_invalid")
		}
		if candidate.MimeType != "image/jpeg" && candidate.MimeType != "image/png" {
			return fmt.Errorf("mime_type_invalid")
		}
		if candidate.DeclaredWidth < 1 || candidate.DeclaredWidth > 10000 || candidate.DeclaredHeight < 1 || candidate.DeclaredHeight > 10000 {
			return fmt.Errorf("declared_dimensions_invalid")
		}
		if _, err := decodeAndVerify(candidate.BytesBase64, candidate.SHA256Base64); err != nil {
			return err
		}
	}
	return nil
}

func decodeAndVerify(payload, digest string) ([]byte, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("undecodable_bytes_base64")
	}
	declared, err := base64.StdEncoding.Strict().DecodeString(digest)
	if err != nil || len(declared) != sha256.Size {
		return nil, fmt.Errorf("undecodable_sha256_base64")
	}
	actual := sha256.Sum256(raw)
	if string(actual[:]) != string(declared) {
		return nil, fmt.Errorf("digest_mismatch")
	}
	return raw, nil
}

func writeCode(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
}
