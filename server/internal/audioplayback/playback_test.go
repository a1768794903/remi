package audioplayback

import (
	"bytes"
	"testing"
)

func TestParseRangeSupportsOpenEndedAndSuffixRanges(t *testing.T) {
	cases := []struct {
		header string
		start  int64
		end    int64
	}{
		{header: "bytes=0-3", start: 0, end: 3},
		{header: "bytes=4-", start: 4, end: 9},
		{header: "bytes=-3", start: 7, end: 9},
	}
	for _, tc := range cases {
		start, end, ok := ParseRange(tc.header, 10)
		if !ok || start != tc.start || end != tc.end {
			t.Fatalf("%s: got %d-%d ok=%v", tc.header, start, end, ok)
		}
	}
	if _, _, ok := ParseRange("bytes=10-11", 10); ok {
		t.Fatal("accepted a range starting at EOF")
	}
}

func TestPCMToWAVWritesCanonicalHeader(t *testing.T) {
	wav, err := PCMToWAV(bytes.Repeat([]byte{0}, 4), 16000, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(wav) != 48 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		t.Fatalf("unexpected WAV output: len=%d header=%q", len(wav), wav[:12])
	}
}
