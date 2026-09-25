package screenframes

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"math"

	"golang.org/x/image/draw"
)

type Ground struct {
	Stops     []string `json:"stops"`
	IsNeutral bool     `json:"is_neutral"`
}

func ComputeGround(jpegBytes []byte) Ground {
	neutral := Ground{Stops: []string{"#59627A", "#333A55"}, IsNeutral: true}
	src, _, err := image.Decode(bytes.NewReader(jpegBytes))
	if err != nil {
		return neutral
	}
	dst := image.NewRGBA(image.Rect(0, 0, 16, 16))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	samples := make([]color.RGBA, 0, 256)
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			samples = append(samples, dst.RGBAAt(x, y))
		}
	}
	return groundFromSamples(samples)
}

func groundFromSamples(samples []color.RGBA) Ground {
	neutral := Ground{Stops: []string{"#59627A", "#333A55"}, IsNeutral: true}
	if len(samples) == 0 {
		return neutral
	}
	const binsCount = 24
	bins, xs, ys := [binsCount]float64{}, [binsCount]float64{}, [binsCount]float64{}
	total := 0.0
	for _, sample := range samples {
		r, g, b := float64(sample.R)/255, float64(sample.G)/255, float64(sample.B)/255
		h, s, v := hsv(r, g, b)
		midness := 1 - math.Abs(v-0.5)*2
		weight := s * s * math.Max(0, midness)
		if weight <= 0 {
			continue
		}
		bin := int(h * float64(binsCount))
		if bin >= binsCount {
			bin = binsCount - 1
		}
		bins[bin] += weight
		angle := h * 2 * math.Pi
		xs[bin] += math.Cos(angle) * weight
		ys[bin] += math.Sin(angle) * weight
		total += weight
	}
	winner := 0
	for i := 1; i < binsCount; i++ {
		if bins[i] > bins[winner] {
			winner = i
		}
	}
	if total < 0.045*float64(len(samples)) || bins[winner] <= 0 {
		return neutral
	}
	h := math.Atan2(ys[winner], xs[winner]) / (2 * math.Pi)
	if h < 0 {
		h += 1
	}
	brightness, saturation := 0.46, 0.42
	for brightness > 0.16 && contrastWhite(h, saturation, brightness) < 4.5 {
		brightness -= 0.02
	}
	return Ground{Stops: []string{hexHSV(h, saturation, brightness), hexHSV(math.Mod(h+0.06, 1), saturation+0.13, math.Max(0.12, brightness-0.18))}, IsNeutral: false}
}

func hsv(r, g, b float64) (h, s, v float64) {
	max, min := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	v = max
	d := max - min
	if max != 0 {
		s = d / max
	}
	if d == 0 {
		return 0, s, v
	}
	switch max {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h / 6, s, v
}
func rgb(h, s, v float64) (float64, float64, float64) {
	i := math.Floor(h * 6)
	f := h*6 - i
	p := v * (1 - s)
	q := v * (1 - f*s)
	t := v * (1 - (1-f)*s)
	switch int(i) % 6 {
	case 0:
		return v, t, p
	case 1:
		return q, v, p
	case 2:
		return p, v, t
	case 3:
		return p, q, v
	case 4:
		return t, p, v
	default:
		return v, p, q
	}
}
func hexHSV(h, s, v float64) string {
	r, g, b := rgb(h, s, v)
	return fmt.Sprintf("#%02X%02X%02X", int(math.Round(r*255)), int(math.Round(g*255)), int(math.Round(b*255)))
}
func contrastWhite(h, s, v float64) float64 {
	r, g, b := rgb(h, s, v)
	lum := func(c float64) float64 {
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	l := 0.2126*lum(r) + 0.7152*lum(g) + 0.0722*lum(b)
	return 1.05 / (l + 0.05)
}
