package screenframes

import (
	"crypto/sha256"
	"encoding/base64"
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
