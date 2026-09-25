package screenframes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"remi/server/internal/auth"
)

type Handler struct{ Judge Judge }

type Judge interface {
	Judge(context.Context, string, []byte) (Judgement, error)
}
type Judgement struct {
	Outcome           string   `json:"outcome"`
	RejectReason      *string  `json:"reject_reason"`
	Caption           string   `json:"caption"`
	Labels            []string `json:"labels"`
	SourceBadge       *string  `json:"source_badge"`
	BannerSuitability float64  `json:"banner_suitability"`
}

type HTTPJudge struct {
	Endpoint string
	Client   *http.Client
}

func (j HTTPJudge) Judge(ctx context.Context, uid string, jpegBytes []byte) (Judgement, error) {
	payload, _ := json.Marshal(map[string]any{"uid": uid, "purpose": "meeting_note_v1", "image_base64": base64.StdEncoding.EncodeToString(jpegBytes), "mime_type": "image/jpeg"})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, j.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return Judgement{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := j.Client
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return Judgement{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Judgement{}, fmt.Errorf("judge returned %s", response.Status)
	}
	var result Judgement
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return Judgement{}, err
	}
	if err := validateJudgement(result); err != nil {
		return Judgement{}, err
	}
	return result, nil
}

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

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
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
	judge := h.Judge
	if judge == nil && strings.TrimSpace(os.Getenv("SCREEN_FRAME_JUDGE_ENDPOINT")) != "" {
		judge = HTTPJudge{Endpoint: strings.TrimSpace(os.Getenv("SCREEN_FRAME_JUDGE_ENDPOINT"))}
	}
	if judge == nil {
		writeCode(w, http.StatusServiceUnavailable, "judge_unavailable")
		return
	}
	for _, candidate := range in.Candidates {
		raw, _ := decodeAndVerify(candidate.BytesBase64, candidate.SHA256Base64)
		canonical, err := canonicalJPEG(raw)
		if err != nil {
			continue
		}
		judgement, err := judge.Judge(r.Context(), uid, canonical)
		if err != nil {
			continue
		}
		if judgement.Outcome == "approved_clean" {
			writeCode(w, http.StatusNotImplemented, "screen_frame_writer_not_implemented")
			return
		}
	}
	writeCode(w, http.StatusOK, "no_approved_frames")
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

func canonicalJPEG(raw []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width*cfg.Height > 25_000_000 {
		return nil, fmt.Errorf("canonicalization_failed")
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("canonicalization_failed")
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, decoded, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func validateJudgement(j Judgement) error {
	if j.Outcome != "approved_clean" && j.Outcome != "rejected" {
		return fmt.Errorf("invalid_judge_outcome")
	}
	if j.Outcome == "approved_clean" && j.RejectReason != nil {
		return fmt.Errorf("contradictory_judge_output")
	}
	if j.Outcome == "rejected" && j.RejectReason == nil {
		return fmt.Errorf("contradictory_judge_output")
	}
	if len(j.Caption) > 160 || len(j.Labels) > 8 || j.BannerSuitability < 0 || j.BannerSuitability > 1 {
		return fmt.Errorf("invalid_judge_metadata")
	}
	return nil
}

func writeCode(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
}
