package releases

import "testing"

func TestParseBetaCandidateTag(t *testing.T) {
	got, err := parseBetaCandidateTag("v1.2.3+45-macos")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tag != "v1.2.3+45-macos" || got.Build != 45 || got.Major != 1 || got.Minor != 2 || got.Patch != 3 {
		t.Fatalf("unexpected candidate: %#v", got)
	}
	for _, tag := range []string{"1.2.3+45-macos", "v1.2.3+0-macos", "v1.2.3+45-windows", "v1.2-macos"} {
		if _, err := parseBetaCandidateTag(tag); err == nil {
			t.Fatalf("tag %q should be rejected", tag)
		}
	}
}

func TestBetaControlReservationAndAdmissionCAS(t *testing.T) {
	control := betaControl{PromotionEnabled: false, Generation: 0}
	var err error
	control, err = reserveBetaCandidate(control, "v1.2.3+45-macos")
	if err != nil || control.Generation != 1 {
		t.Fatalf("initial reservation = %#v, err=%v", control, err)
	}
	control.PromotionEnabled = true
	control, err = reserveBetaCandidate(control, "v1.2.3+45-macos")
	if err != nil || control.Generation != 1 || control.ReservedTag != "v1.2.3+45-macos" {
		t.Fatalf("first reservation = %#v, err=%v", control, err)
	}
	control, err = reserveBetaCandidate(control, "v1.2.3+45-macos")
	if err != nil || control.Generation != 1 {
		t.Fatalf("idempotent reservation = %#v, err=%v", control, err)
	}
	if _, err = reserveBetaCandidate(control, "v1.2.3+44-macos"); err == nil {
		t.Fatal("reservation must not roll back build number")
	}
	if _, err = captureBetaAdmission(control, "v1.2.3+46-macos"); err == nil {
		t.Fatal("capture must require the reserved candidate")
	}
	if _, err = captureBetaAdmission(control, "v1.2.3+45-macos"); err != nil {
		t.Fatal(err)
	}
}

func TestBetaAdmissionToggleRequiresReservationToResume(t *testing.T) {
	control := betaControl{}
	if _, err := setBetaAdmission(control, true); err == nil {
		t.Fatal("admission cannot resume without a reservation")
	}
	control, err := setBetaAdmission(control, false)
	if err != nil || control.Generation != 1 || control.PromotionEnabled {
		t.Fatalf("initial pause = %#v, err=%v", control, err)
	}
}
