package screenframes

import (
	"testing"
	"time"
)

func TestApprovalRoundTripAndFences(t *testing.T) {
	t.Setenv("SCREEN_FRAME_APPROVAL_SECRET", "01234567890123456789012345678901")
	now := time.Unix(1_700_000_000, 0)
	token, err := mintApproval("u1", "meeting_note_v1", "c1", []byte("jpeg"), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyApproval(token, "u1", "meeting_note_v1", "c1", []byte("jpeg"), now); err != nil {
		t.Fatal(err)
	}
	if err := verifyApproval(token, "u2", "meeting_note_v1", "c1", []byte("jpeg"), now); err == nil {
		t.Fatal("uid fence missing")
	}
	if err := verifyApproval(token, "u1", "meeting_note_v1", "c1", []byte("other"), now); err == nil {
		t.Fatal("digest fence missing")
	}
}

func TestApprovalRequiresLongSecret(t *testing.T) {
	t.Setenv("SCREEN_FRAME_APPROVAL_SECRET", "short")
	if _, err := mintApproval("u", "p", "s", []byte("x"), time.Now()); err == nil {
		t.Fatal("short secret accepted")
	}
}
