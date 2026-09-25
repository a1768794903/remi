package integrations

import "testing"

func TestConvertGoogleEventPreservesAttendeesAndBoundsText(t *testing.T) {
	event, ok := convertGoogleEvent(map[string]any{"id": "e1", "summary": "Meeting", "start": map[string]any{"dateTime": "2026-01-01T10:00:00Z"}, "end": map[string]any{"dateTime": "2026-01-01T11:00:00Z"}, "attendees": []any{map[string]any{"displayName": "Ada", "email": "ada@example.com"}}, "location": string(make([]byte, 300))})
	if !ok || event.EventID != "e1" || len(event.Attendees) != 1 || len([]rune(event.Location)) != 200 {
		t.Fatalf("unexpected event: %+v ok=%v", event, ok)
	}
}

func TestConvertGoogleEventRejectsMissingOrNonPositiveTime(t *testing.T) {
	if _, ok := convertGoogleEvent(map[string]any{"start": map[string]any{"date": "2026-01-01"}, "end": map[string]any{"date": "2026-01-01"}}); ok {
		t.Fatal("zero-duration event should be ignored")
	}
}
