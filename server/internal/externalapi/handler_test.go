package externalapi

import (
	"testing"
	"time"
)

func TestParseExternalDateSupportsDayAndRFC3339(t *testing.T) {
	start, err := parseExternalDate("2026-09-25", false)
	if err != nil || !start.Equal(time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("start=%v err=%v", start, err)
	}
	end, err := parseExternalDate("2026-09-25", true)
	if err != nil || end.Hour() != 23 || end.Minute() != 59 || end.Nanosecond() != 999999999 {
		t.Fatalf("end=%v err=%v", end, err)
	}
	parsed, err := parseExternalDate("2026-09-25T12:30:00+08:00", false)
	if err != nil || parsed.UTC().Hour() != 4 {
		t.Fatalf("parsed=%v err=%v", parsed, err)
	}
}

func TestParseExternalDateRejectsInvalidDate(t *testing.T) {
	if _, err := parseExternalDate("not-a-date", false); err == nil {
		t.Fatal("invalid date accepted")
	}
}
