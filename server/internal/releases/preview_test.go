package releases

import "testing"

func validPreviewManifest() previewManifest {
	id := previewIdentity("feature-one")
	return previewManifest{
		Slug: "feature-one", SourceSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		DMGURL:    "https://storage.googleapis.com/omi_macos_updates/previews/feature-one/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/Omi-Preview.dmg",
		DMGSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		AppName:   "Omi Preview feature-one", BundleID: "com.omi.preview." + id, URLScheme: "omi-preview-" + id,
		BuiltAt: "2026-09-25T00:00:00Z", Signer: "ci", Notarization: "stapled",
	}
}

func TestValidatePreviewManifestEnforcesImmutableIdentity(t *testing.T) {
	manifest := validPreviewManifest()
	if err := validatePreviewManifest(manifest); err != nil {
		t.Fatal(err)
	}
	manifest.DMGURL = "https://example.com/Omi-Preview.dmg"
	if err := validatePreviewManifest(manifest); err == nil {
		t.Fatal("non-canonical preview URL should be rejected")
	}
	manifest = validPreviewManifest()
	manifest.BundleID = "com.omi.preview.other"
	if err := validatePreviewManifest(manifest); err == nil {
		t.Fatal("slug identity mismatch should be rejected")
	}
}
