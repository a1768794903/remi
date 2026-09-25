package framerequests

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"remi/server/internal/signedurl"
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

func TestSharedScreenFrameResponseUsesOwnerBoundSignedURL(t *testing.T) {
	t.Setenv("SCREEN_FRAME_URL_SECRET", strings.Repeat("s", 32))
	got := screenFrameSetForUID("owner-1", "conversation-1", []map[string]any{{"id": "frame-1"}}, 4, true)
	frames := got["strip"].([]map[string]any)
	contentURL := frames[0]["content_url"].(string)
	parsed, err := url.Parse(contentURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/v1/conversations/conversation-1/shared/screenshots/frame-1/image" {
		t.Fatalf("path = %q", parsed.Path)
	}
	if !signedurl.Verify(parsed.Path, "owner-1", parsed.Query(), time.Now().UTC()) {
		t.Fatal("shared URL is not valid for its owner")
	}
	if signedurl.Verify(parsed.Path, "other-user", parsed.Query(), time.Now().UTC()) {
		t.Fatal("shared URL must not be valid for another user")
	}
}

func TestTemporaryImageReadsUploadedUnattachedFrame(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Add(time.Hour)
	mock.ExpectQuery("SELECT request_id,user_external_uid,device_id,account_generation").
		WithArgs("frame-1", "user-1").
		WillReturnRows(sqlmock.NewRows([]string{"request_id", "user_external_uid", "device_id", "account_generation", "dedupe_key", "dedupe_window", "attempt_number", "conversation_id", "screenshot_id", "state", "created_at", "expires_at", "claimed_at", "uploaded_at", "attached_at", "terminal_reason", "byte_count", "content_type", "storage_id", "cleanup_state", "cleanup_attempts", "cleanup_next_attempt_at"}).
			AddRow("frame-1", "user-1", "mac", 7, "d", 0, 0, nil, nil, "uploaded", now.Add(-time.Minute), now, now, now.Add(-time.Minute), now, nil, 3, "image/jpeg", "storage-1", "pending", 0, now))
	h := Handler{DB: db, Store: staticStore{data: []byte("jpeg")}}
	r := httptest.NewRequest(http.MethodGet, "/v1/frame-requests/temporary/frame-1/image?account_generation=7", nil)
	w := httptest.NewRecorder()
	h.temporaryImage(w, r, "user-1")
	if w.Code != http.StatusOK || w.Body.String() != "jpeg" || w.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("response = %d %q %q", w.Code, w.Body.String(), w.Header().Get("Content-Type"))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

type staticStore struct{ data []byte }

func (s staticStore) Put(context.Context, string, string, []byte) error   { return nil }
func (s staticStore) Get(context.Context, string, string) ([]byte, error) { return s.data, nil }
func (s staticStore) Delete(context.Context, string, string) error        { return nil }
func (s staticStore) Copy(context.Context, string, string, string) error  { return nil }

var _ Store = staticStore{}
