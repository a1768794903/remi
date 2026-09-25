package screenframes

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestGroundIsNeutralForAchromaticImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{180, 180, 180, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)
	ground := ComputeGround(b.Bytes())
	if !ground.IsNeutral || len(ground.Stops) != 2 {
		t.Fatalf("ground=%+v", ground)
	}
}

func TestGroundFindsChromaticImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{30, 90, 190, 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)
	ground := ComputeGround(b.Bytes())
	if ground.IsNeutral || len(ground.Stops) != 2 {
		t.Fatalf("ground=%+v", ground)
	}
}
