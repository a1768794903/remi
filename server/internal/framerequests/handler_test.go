package framerequests

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
	"time"
)

func TestCanonicalImageBoundsAndJPEGOutput(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 32; x++ {
			source.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var input bytes.Buffer
	if err := jpeg.Encode(&input, source, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	out, contentType, err := canonicalImage(input.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "image/jpeg" || len(out) == 0 {
		t.Fatalf("unexpected canonical output: %q, %d bytes", contentType, len(out))
	}
	if _, _, err := image.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("canonical output is not decodable: %v", err)
	}
}

func TestRetryDelayIsBoundedAndExponential(t *testing.T) {
	if retryDelay(0) != time.Second || retryDelay(3) != 8*time.Second {
		t.Fatal("unexpected retry backoff")
	}
	if retryDelay(100) > 24*time.Hour {
		t.Fatal("retry backoff is not bounded")
	}
}

func TestCanonicalImageRejectsInvalidBytes(t *testing.T) {
	if _, _, err := canonicalImage([]byte("not-an-image")); err == nil {
		t.Fatal("invalid image should be rejected")
	}
}

func TestScreenFrameResponseHidesStorageIdentity(t *testing.T) {
	photos := []map[string]any{{"id": "frame-1", "storage_id": "permanent-secret", "content_type": "image/jpeg"}}
	got := screenFrameSet("42", photos, 3)
	if got["revision"] != 3 {
		t.Fatalf("revision = %#v", got["revision"])
	}
	frames, ok := got["strip"].([]map[string]any)
	if !ok || len(frames) != 1 {
		t.Fatalf("strip = %#v", got["strip"])
	}
	if _, exists := frames[0]["storage_id"]; exists {
		t.Fatal("storage_id leaked in public frame response")
	}
	if frames[0]["content_url"] != "/v1/conversations/42/screenshots/frame-1/image" {
		t.Fatalf("content_url = %#v", frames[0]["content_url"])
	}
}
