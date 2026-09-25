package syncjobs

import "testing"

func TestCodecFromNamePreservesSupportedAudioContracts(t *testing.T) {
	cases := map[string]string{"capture_pcm16_001.bin": "pcm16", "capture_pcm8_001.bin": "pcm8", "capture_opus_001.bin": "opus", "capture_unknown.bin": "opus"}
	for name, want := range cases {
		if got := codecFromName(name); got != want {
			t.Fatalf("%s: got %s want %s", name, got, want)
		}
	}
}

func TestNewJobDefaultsToFreshQueuedLane(t *testing.T) {
	j := NewJob("uid", "42", nil)
	if j.UID != "uid" || j.ConversationID != "42" || j.Status != "queued" || j.Lane != "fresh" || j.ID == "" {
		t.Fatalf("unexpected job: %+v", j)
	}
}
