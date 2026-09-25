package screenframes

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestValidateAcceptsStrictTransportDigest(t *testing.T) {
	raw := []byte("candidate")
	sum := sha256.Sum256(raw)
	in := Request{SchemaVersion: 1, AttemptID: uuid.New(), Purpose: "meeting_note_v1", Subject: Subject{Kind: "conversation", ID: "c1"}, Candidates: []Candidate{{ClientFrameID: "f1", MimeType: "image/jpeg", DeclaredWidth: 1, DeclaredHeight: 1, SHA256Base64: base64.StdEncoding.EncodeToString(sum[:]), BytesBase64: base64.StdEncoding.EncodeToString(raw)}}}
	if err := Validate(in); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsDigestMismatchAndTooManyCandidates(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte("candidate"))
	candidate := Candidate{ClientFrameID: "f1", MimeType: "image/jpeg", DeclaredWidth: 1, DeclaredHeight: 1, SHA256Base64: base64.StdEncoding.EncodeToString(make([]byte, sha256.Size)), BytesBase64: raw}
	in := Request{SchemaVersion: 1, AttemptID: uuid.New(), Purpose: "meeting_note_v1", Subject: Subject{Kind: "conversation", ID: "c1"}, Candidates: []Candidate{candidate}}
	if err := Validate(in); err == nil || err.Error() != "digest_mismatch" {
		t.Fatalf("err=%v", err)
	}
	in.Candidates = make([]Candidate, 9)
	if err := Validate(in); err == nil || err.Error() != "candidate_count_invalid" {
		t.Fatalf("err=%v", err)
	}
}

func TestHTTPJudgeValidatesStructuredDecision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if json.NewDecoder(r.Body).Decode(&input) != nil || input["image_base64"] == "" {
			t.Error("judge payload missing image")
		}
		_ = json.NewEncoder(w).Encode(Judgement{Outcome: "approved_clean", Caption: "shared slide", Labels: []string{"slides"}, BannerSuitability: 0.8})
	}))
	defer server.Close()
	got, err := (HTTPJudge{Endpoint: server.URL}).Judge(t.Context(), "u1", []byte("jpeg"))
	if err != nil || got.Outcome != "approved_clean" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if err := validateJudgement(Judgement{Outcome: "approved_clean", RejectReason: stringPtr("email")}); err == nil {
		t.Fatal("contradictory approval should fail")
	}
}

func stringPtr(value string) *string { return &value }
