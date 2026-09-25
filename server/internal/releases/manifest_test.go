package releases

import "testing"

func validTestManifest() releaseManifest {
	return releaseManifest{
		ReleaseID: "v1.2.3+45-macos", Platform: "macos", Channel: "beta", Version: "1.2.3+45",
		BuildNumber: 45, ZipURL: "https://github.com/BasedHardware/omi/releases/download/v1.2.3+45-macos/Omi.zip",
		DMGURL:      "https://github.com/BasedHardware/omi/releases/download/v1.2.3+45-macos/omi.dmg",
		EDSignature: "signature", AppSourceSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		QualificationTier: "signed-smoke",
	}
}

func TestValidateReleaseManifestRejectsUnsafeOrIncompleteAssets(t *testing.T) {
	manifest := validTestManifest()
	if _, _, err := canonicalManifestJSON(manifest); err != nil {
		t.Fatal(err)
	}
	manifest.ZipURL = "http://example.com/Omi.zip"
	if _, _, err := canonicalManifestJSON(manifest); err == nil {
		t.Fatal("non-GitHub asset should be rejected")
	}
	manifest = validTestManifest()
	manifest.DMGURL = ""
	if _, _, err := canonicalManifestJSON(manifest); err == nil {
		t.Fatal("macOS manifest without dmg should be rejected")
	}
}

func TestManifestDigestIsStable(t *testing.T) {
	manifest := validTestManifest()
	_, first, err := canonicalManifestJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := canonicalManifestJSON(manifest)
	if err != nil || first != second {
		t.Fatalf("digest changed: %q vs %q, err=%v", first, second, err)
	}
}

func TestBuildBetaPointerUsesCASAndRollForward(t *testing.T) {
	manifest := validTestManifest()
	current := channelPointer{Platform: "macos", Channel: "beta", ReleaseID: "old", Version: "1.0.0+40", Build: 40, Generation: 7}
	wantGeneration := int64(7)
	got, err := buildBetaPointer(current, manifest, &wantGeneration)
	if err != nil || got.Generation != 8 || got.ReleaseID != manifest.ReleaseID {
		t.Fatalf("pointer = %#v, err=%v", got, err)
	}
	wrong := int64(6)
	if _, err := buildBetaPointer(current, manifest, &wrong); err == nil {
		t.Fatal("stale generation should be rejected")
	}
	old := manifest
	old.BuildNumber = 39
	if _, err := buildBetaPointer(current, old, &wantGeneration); err == nil {
		t.Fatal("rollback build should be rejected")
	}
	if got, err := buildBetaPointer(channelPointer{Platform: "macos", Channel: "beta", ReleaseID: manifest.ReleaseID, Generation: 8}, manifest, nil); err != nil || got.Generation != 8 {
		t.Fatalf("same release retry should be idempotent: %#v, %v", got, err)
	}
}

func TestBuildBreakglassPointerRequiresExactCAS(t *testing.T) {
	manifest := validTestManifest()
	current := channelPointer{Platform: "macos", Channel: "beta", ReleaseID: "old", Build: 60, Generation: 9}
	got, err := buildBreakglassPointer(current, manifest, 9, "old")
	if err != nil || got.Generation != 10 {
		t.Fatalf("breakglass pointer = %#v, err=%v", got, err)
	}
	if _, err := buildBreakglassPointer(current, manifest, 8, "old"); err == nil {
		t.Fatal("stale generation should be rejected")
	}
	if _, err := buildBreakglassPointer(current, manifest, 9, "other"); err == nil {
		t.Fatal("wrong current release should be rejected")
	}
}
