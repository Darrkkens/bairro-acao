package similarity

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand/v2"
	"testing"
)

// scene draws a synthetic photo: a tiled pavement or a noisy green field.
func scene(t *testing.T, kind string, w, h, shift int, seed uint64) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewPCG(seed, 1))
	for y := range h {
		for x := range w {
			var c color.RGBA
			switch kind {
			case "tiles":
				v := uint8(120 + rng.IntN(20))
				if (x+shift)%40 < 3 || (y+shift)%40 < 3 {
					v = 60
				}
				c = color.RGBA{v, v, v, 255}
			case "grass":
				c = color.RGBA{uint8(30 + rng.IntN(60)), uint8(110 + rng.IntN(90)), uint8(20 + rng.IntN(40)), 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func signature(t *testing.T, data []byte) Signature {
	t.Helper()
	s, err := Compute(data)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSameShotIsADuplicate(t *testing.T) {
	a := signature(t, scene(t, "tiles", 640, 480, 0, 1))
	resent := signature(t, scene(t, "tiles", 640, 480, 0, 1))
	if s := Compare(a, resent); !s.Duplicate() {
		t.Fatalf("identical photo not a duplicate: %+v", s)
	}
	moved := signature(t, scene(t, "tiles", 640, 480, 17, 2))
	if s := Compare(a, moved); s.Duplicate() || s.Overall < 0.8 {
		t.Fatalf("same surface, other framing: want similar but not duplicate, got %+v", s)
	}
	grass := signature(t, scene(t, "grass", 640, 480, 0, 3))
	if s := Compare(a, grass); s.Overall > 0.6 || s.Duplicate() {
		t.Fatalf("pavement vs grass scored %+v", s)
	}
}

func TestSignatureRoundTrip(t *testing.T) {
	s := signature(t, scene(t, "grass", 200, 150, 0, 4))
	raw, _ := s.MarshalBinary()
	var back Signature
	if err := back.UnmarshalBinary(raw); err != nil || back != s {
		t.Fatalf("round trip changed the signature: %v", err)
	}
	if err := back.UnmarshalBinary(raw[:10]); err == nil {
		t.Error("truncated signature accepted")
	}
}

func TestRejectsNonImages(t *testing.T) {
	if _, err := Compute([]byte{0xFF, 0xD8, 0xFF, 0x00}); err == nil {
		t.Error("broken JPEG accepted")
	}
}
