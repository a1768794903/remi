package users

import (
	"testing"
	"time"

	"remi/server/ent"
)

func TestNotificationSettingsDefaultsAndReadsJSONNumbers(t *testing.T) {
	defaults := notificationSettings(&ent.User{})
	if !defaults.Enabled || defaults.Frequency != 0 {
		t.Fatalf("unexpected defaults: %#v", defaults)
	}
	settings := notificationSettings(&ent.User{NotificationSettings: map[string]any{"enabled": false, "frequency": float64(3)}})
	if settings.Enabled || settings.Frequency != 3 {
		t.Fatalf("unexpected persisted settings: %#v", settings)
	}
}

func TestDecodeOnboardingPreservesPythonDefaultsAndFields(t *testing.T) {
	if got := decodeOnboarding(nil); got != (OnboardingState{}) {
		t.Fatalf("nil onboarding should use zero-compatible defaults: %#v", got)
	}
	got := decodeOnboarding(map[string]any{
		"completed":                   true,
		"acquisition_source":          "desktop",
		"device_onboarding_completed": true,
	})
	want := OnboardingState{Completed: true, AcquisitionSource: "desktop", DeviceOnboardingCompleted: true}
	if got != want {
		t.Fatalf("decoded onboarding = %#v, want %#v", got, want)
	}
}

func TestScreenFrameSettingsDefaultIsEnabled(t *testing.T) {
	if !(&ent.User{MeetingNoteScreenshotsEnabled: true}).MeetingNoteScreenshotsEnabled {
		t.Fatal("screen-frame setting must default to enabled for existing Python users")
	}
}

func TestGeolocationValidationAndRoundingContract(t *testing.T) {
	if !validGeolocation(geolocationInput{Latitude: 90, Longitude: -180, CaptureSource: "manual"}) {
		t.Fatal("valid boundary coordinates rejected")
	}
	if validGeolocation(geolocationInput{Latitude: 90.0001, Longitude: 0}) {
		t.Fatal("latitude outside bounds accepted")
	}
	if validGeolocation(geolocationInput{Latitude: 0, Longitude: 0, CaptureSource: "unknown"}) {
		t.Fatal("unknown capture source accepted")
	}
	if roundGeo(12.34567) != 12.3457 {
		t.Fatalf("roundGeo = %v", roundGeo(12.34567))
	}
}

func TestDeveloperWebhookURLValidationAndTypes(t *testing.T) {
	if !webhookConfigured("memory_created", "https://example.test/hook") {
		t.Fatal("HTTPS webhook should be accepted")
	}
	if !webhookConfigured("audio_bytes", "https://example.test/hook,5") {
		t.Fatal("audio_bytes URL suffix should be ignored")
	}
	if webhookConfigured("memory_created", "file:///tmp/hook") {
		t.Fatal("non-HTTP webhook should be rejected")
	}
	if developerWebhookTypes["not-a-webhook"] {
		t.Fatal("unknown webhook type accepted")
	}
}

func TestDeveloperWebhookRetryScheduleDefaultsAndOverride(t *testing.T) {
	t.Setenv("DEV_WEBHOOK_RETRY_DELAYS", "0,0.25")
	got := webhookRetryDelays()
	if len(got) != 2 || got[0] != 0 || got[1] != 250*time.Millisecond {
		t.Fatalf("retry delays = %#v", got)
	}
	t.Setenv("DEV_WEBHOOK_RETRY_DELAYS", "bad")
	got = webhookRetryDelays()
	if len(got) != 3 || got[1] != 5*time.Second {
		t.Fatalf("invalid override should use defaults: %#v", got)
	}
}
