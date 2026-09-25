package desktopusage

import "testing"

func TestValidateDateWindowRejectsFarDates(t *testing.T) {
	if err := validate("2000-01-01", "UTC", "device"); err == nil {
		t.Fatal("expected date window rejection")
	}
}

func TestValidateAcceptsValidPayload(t *testing.T) {
	if err := validate("2026-09-25", "UTC", "device"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
