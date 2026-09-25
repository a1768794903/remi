package memories

import "testing"

func TestValidateBatchCreateLimit(t *testing.T) {
	if err := validateBatchCreateCount(0); err != nil {
		t.Fatalf("empty batch should be accepted: %v", err)
	}
	if err := validateBatchCreateCount(200); err != nil {
		t.Fatalf("maximum batch should be accepted: %v", err)
	}
	if err := validateBatchCreateCount(201); err == nil {
		t.Fatal("oversized batch should be rejected")
	}
}
