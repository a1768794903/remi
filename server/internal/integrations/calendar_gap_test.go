package integrations

import (
	"testing"
	"time"
)

func TestParseCalendarQueryTimeTreatsNaiveTimeAsUTC(t *testing.T) {
	value, err := parseCalendarQueryTime("2026-01-01T10:00:00")
	if err != nil || value.Location() != time.UTC {
		t.Fatalf("value=%v err=%v", value, err)
	}
}
