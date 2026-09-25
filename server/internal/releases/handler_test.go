package releases

import "testing"

func TestAppcastXMLEscapesReleaseMetadataAndFiltersChannels(t *testing.T) {
	xml := AppcastXML([]Release{{Version: "1<&", BuildNumber: 4, DownloadURL: "https://x.test/a?x=1&y=2", Signature: "sig\"", PublishedAt: "today", Changelog: []string{"<fix>"}, Live: true, Channel: "beta"}, {Version: "old", BuildNumber: 3, Live: false, Channel: "stable"}}, "macos")
	for _, want := range []string{"Omi 1&lt;&amp;", "sparkle:version>4", "https://x.test/a?x=1&amp;y=2", "&lt;fix&gt;", "sparkle:channel>beta"} {
		if !contains(xml, want) {
			t.Fatalf("appcast missing %q: %s", want, xml)
		}
	}
	if contains(xml, "old") {
		t.Fatalf("non-live release leaked into appcast: %s", xml)
	}
}

func TestManualDownloadURLUsesDMGForOmiZip(t *testing.T) {
	got := manualDownloadURL(Release{DownloadURL: "https://x/Omi.zip"})
	if got != "https://x/Omi.dmg" {
		t.Fatalf("got %q", got)
	}
}

func TestLatestReleaseForChannelSkipsNonLiveAndPrefersNewest(t *testing.T) {
	releases := []Release{
		{Version: "stable-old", BuildNumber: 10, Live: true, Channel: "stable"},
		{Version: "beta-new", BuildNumber: 30, Live: true, Channel: "beta"},
		{Version: "beta-hidden", BuildNumber: 40, Live: false, Channel: "beta"},
		{Version: "beta-old", BuildNumber: 20, Live: true, Channel: "beta"},
	}
	got, ok := latestReleaseForChannel(releases, "beta")
	if !ok || got.Version != "beta-new" {
		t.Fatalf("latest beta = %#v, %v", got, ok)
	}
	if _, ok := latestReleaseForChannel(releases, "canary"); ok {
		t.Fatal("unexpected release for unknown channel")
	}
}

func TestDefaultUpdatePolicyIsInactiveAndUsesLatestDownloadRoute(t *testing.T) {
	policy := defaultUpdatePolicy()
	if policy.Active || policy.Severity != "none" || policy.DownloadURL != "/v2/desktop/download/latest" {
		t.Fatalf("unexpected default policy: %#v", policy)
	}
	if !policy.CanDismiss || policy.CTAText != "Download latest" {
		t.Fatalf("unexpected policy controls: %#v", policy)
	}
}

func TestLatestPlatformReleaseUsesBetaFallbackOnlyWhenRequested(t *testing.T) {
	releases := []Release{
		{Platform: "windows", Channel: "stable", Live: true, BuildNumber: 10, Version: "1.0"},
		{Platform: "windows", Channel: "beta", Live: true, BuildNumber: 20, Version: "2.0"},
	}
	got, served, ok := latestPlatformRelease(releases, "windows", "beta", true)
	if !ok || served != "beta" || got.BuildNumber != 20 {
		t.Fatalf("beta = %#v, %q, %v", got, served, ok)
	}
	got, served, ok = latestPlatformRelease([]Release{{Platform: "windows", Channel: "stable", Live: true, BuildNumber: 10}}, "windows", "beta", true)
	if !ok || served != "stable" || got.BuildNumber != 10 {
		t.Fatalf("fallback = %#v, %q, %v", got, served, ok)
	}
}

func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
