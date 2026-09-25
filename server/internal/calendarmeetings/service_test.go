package calendarmeetings

import (
	"testing"
	"time"
)

func TestNaturalKeyIsStableAndUIDScoped(t *testing.T) {
	a := naturalKey("uid-a", "google", "event-1")
	if a == "" || a != naturalKey("uid-a", "google", "event-1") {
		t.Fatal("natural key is not stable")
	}
	if a == naturalKey("uid-b", "google", "event-1") || a == naturalKey("uid-a", "outlook", "event-1") {
		t.Fatal("natural key is not scoped")
	}
}

func TestMeetingContextDuration(t *testing.T) {
	start := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	end := start.Add(90 * time.Minute)
	if got := durationMinutes(start, end); got != 90 {
		t.Fatalf("duration = %d", got)
	}
}
