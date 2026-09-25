package people

import "testing"

func TestValidatePersonNameMatchesPythonBounds(t *testing.T) {
	if validateName("a") == nil || validateName("valid") != nil || validateName("12345678901234567890123456789012345678901") == nil {
		t.Fatal("name bounds are incorrect")
	}
}

func TestVoiceReadinessWithoutSamplesIsNotLearned(t *testing.T) {
	if readiness([]string{}, 3, nil) != "not_learned" {
		t.Fatal("expected not_learned")
	}
	if readiness([]string{"sample"}, 3, nil) != "saved_sample_awaiting_embedding" {
		t.Fatal("expected awaiting embedding")
	}
}
