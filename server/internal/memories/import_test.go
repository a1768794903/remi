package memories

import "testing"

func TestMemoryImportIDsAreDeterministicAndSourceIsNormalized(t *testing.T) {
	if got := normalizeImportSource("  Google-Drive  "); got != "google_drive" {
		t.Fatalf("source=%q", got)
	}
	a := stableImportID("u", "google_drive", "acct", "doc", "hash")
	if a == "" || a != stableImportID("u", "google_drive", "acct", "doc", "hash") {
		t.Fatal("artifact id must be deterministic")
	}
	if a == stableImportID("other", "google_drive", "acct", "doc", "hash") {
		t.Fatal("artifact id must be owner scoped")
	}
}

func TestMemoryImportItemRequiresIdentityOrContent(t *testing.T) {
	if err := validateImportItem(memoryImportItem{}); err == nil {
		t.Fatal("empty artifact should fail")
	}
	if err := validateImportItem(memoryImportItem{ExternalID: "doc-1"}); err != nil {
		t.Fatal(err)
	}
}
