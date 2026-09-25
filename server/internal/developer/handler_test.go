package developer

import "testing"

func TestDeveloperLimitBoundsPagination(t *testing.T) {
	if got := developerLimit(0, 25, 100); got != 25 {
		t.Fatalf("default limit = %d", got)
	}
	if got := developerLimit(101, 25, 100); got != 100 {
		t.Fatalf("maximum limit = %d", got)
	}
	if got := developerOffset(-4); got != 0 {
		t.Fatalf("negative offset = %d", got)
	}
}

func TestDeveloperMemoryContentBounds(t *testing.T) {
	if err := validateMemoryContent(""); err == nil {
		t.Fatal("empty memory content should be rejected")
	}
	if err := validateMemoryContent("ok"); err != nil {
		t.Fatal(err)
	}
	long := make([]byte, 501)
	for i := range long {
		long[i] = 'x'
	}
	if err := validateMemoryContent(string(long)); err == nil {
		t.Fatal("memory content over 500 characters should be rejected")
	}
}
