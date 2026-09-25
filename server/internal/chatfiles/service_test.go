package chatfiles

import "testing"

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

func TestValidateUploadAllowsSupportedDocumentsAndImages(t *testing.T) {
	for _, tc := range []struct{ name, mime string }{
		{"report.pdf", "application/pdf"}, {"notes.txt", "text/plain"}, {"photo.png", "image/png"},
	} {
		if err := validateUpload(tc.name, tc.mime, 1024); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
}

func TestValidateUploadRejectsUnsupportedOrOversizedFiles(t *testing.T) {
	if validateUpload("archive.zip", "application/zip", 1024) == nil {
		t.Fatal("zip accepted")
	}
	if validateUpload("report.pdf", "application/pdf", 51*1024*1024) == nil {
		t.Fatal("oversized file accepted")
	}
}

func TestThumbnailBytesBoundsImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 512, 256))
	src.Set(0, 0, color.White)
	var in bytes.Buffer
	if err := png.Encode(&in, src); err != nil {
		t.Fatal(err)
	}
	out, err := thumbnailBytes(in.Bytes(), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() > 128 || decoded.Bounds().Dy() > 128 {
		t.Fatalf("thumbnail bounds = %v", decoded.Bounds())
	}
}

func TestThumbnailBytesRejectsMalformedImage(t *testing.T) {
	if _, err := thumbnailBytes([]byte("not-an-image"), "image/png"); err == nil {
		t.Fatal("malformed image accepted")
	}
}
