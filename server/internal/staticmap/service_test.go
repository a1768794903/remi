package staticmap

import "testing"

func TestParsePinsBoundsDedupesAndSorts(t *testing.T) {
	pins, err := parsePins("2,3|1,2|2.00001,3.00001")
	if err != nil || len(pins) != 2 || pins[0].Lat != 1 || pins[1].Lat != 2 {
		t.Fatalf("pins = %#v, err=%v", pins, err)
	}
}

func TestParsePinsRejectsOutOfBounds(t *testing.T) {
	if _, err := parsePins("91,0"); err == nil {
		t.Fatal("out-of-bounds pin accepted")
	}
}

func TestStaticMapURLUsesVisibleForMultiplePins(t *testing.T) {
	pins, _ := parsePins("1,2|3,4")
	got := buildURL(pins, 640, 480, "key")
	if !contains(got, "visible=1.0000,2.0000%7C3.0000,4.0000") || !contains(got, "size=640x480") {
		t.Fatalf("url = %s", got)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && stringIndex(s, sub) >= 0 }
func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
