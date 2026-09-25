package signedurl

import (
	"net/url"
	"testing"
	"time"
)

func TestBuildAndVerify(t *testing.T) {
	t.Setenv("SCREEN_FRAME_URL_SECRET", "01234567890123456789012345678901")
	path := "/v1/conversations/c/screenshots/f/image"
	values, err := Build(path, "u", time.Unix(1_700_000_000, 0).Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(values)
	if !Verify(path, "u", parsed.Query(), time.Unix(1_700_000_000, 0)) {
		t.Fatal("signature rejected")
	}
	if Verify(path, "other", parsed.Query(), time.Unix(1_700_000_000, 0)) {
		t.Fatal("uid not fenced")
	}
}
