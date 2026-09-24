package audio

import (
	"testing"
	"time"
)

func TestTrackerCountsPacketsAndRemovesFinishedSession(t *testing.T) {
	tracker := NewTracker()
	now := time.Unix(100, 0)
	tracker.Start("session-1", "user-1", "device-1", "opus_fs320", 16000, now)

	session, ok := tracker.AddPacket("session-1", 320, now.Add(time.Second))
	if !ok || session.ReceivedBytes != 320 || session.ReceivedPackets != 1 {
		t.Fatalf("unexpected session after packet: %+v, ok=%v", session, ok)
	}

	finished, ok := tracker.Finish("session-1")
	if !ok || finished.ID != "session-1" {
		t.Fatalf("unexpected finished session: %+v, ok=%v", finished, ok)
	}
	if _, ok := tracker.AddPacket("session-1", 1, now); ok {
		t.Fatal("finished session accepted a packet")
	}
}
