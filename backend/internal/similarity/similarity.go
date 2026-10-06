// Package similarity tells whether two photos show the same scene, cheaply and
// without AI. It runs before the model: photos of one problem taken seconds
// apart (three shots of the same broken sidewalk) are grouped and analyzed
// once, together, instead of becoming three report entries.
//
// A signature combines classic descriptors that are robust to small changes of
// framing: a color histogram, a texture histogram (local binary patterns, which
// separate patterned tiles from asphalt even when both are grey), an edge
// orientation histogram, and a difference hash for near-identical shots.
package similarity

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	_ "image/jpeg" // phone photos
	_ "image/png"
	"math"
	"math/bits"
)

const (
	side       = 96 // working resolution; enough for texture, cheap to compute
	colorBins  = 8 * 3 * 3
	edgeBins   = 8
	lbpBins    = 10 // uniform LBP patterns (0–8 set bits in a circular run) plus "other"
	signatureN = 8 + 4*(colorBins+edgeBins+lbpBins)
)

// Signature is a compact description of a photo (about 370 bytes).
type Signature struct {
	Hash    uint64 // difference hash of a 9×8 grayscale thumbnail
	Color   [colorBins]float32
	Edges   [edgeBins]float32
	Texture [lbpBins]float32
}

// Compute decodes a JPEG or PNG and describes it.
func Compute(photo []byte) (Signature, error) {
	img, _, err := image.Decode(bytes.NewReader(photo))
	if err != nil {
		return Signature{}, err
	}
	b := img.Bounds()
	if b.Dx() < 16 || b.Dy() < 16 {
		return Signature{}, errors.New("image too small")
	}
	r, g, bl := shrink(img, side, side)
	gray := make([]float64, side*side)
	var s Signature
	for i := range gray {
		gray[i] = 0.299*r[i] + 0.587*g[i] + 0.114*bl[i]
		h, sat, v := hsv(r[i], g[i], bl[i])
		hb := int(h / 45)
		if hb > 7 {
			hb = 7
		}
		sb, vb := min(int(sat*3), 2), min(int(v*3), 2)
		if sat < 0.12 { // grey pixels have no meaningful hue: keep them in one hue bin
			hb = 0
		}
		s.Color[(hb*3+sb)*3+vb]++
	}
	normalize(s.Color[:])
	edges(gray, &s.Edges)
	texture(gray, &s.Texture)
	s.Hash = dhash(img)
	return s, nil
}

// Score is the similarity of two photos in [0, 1], with its parts.
type Score struct {
	Overall, Color, Texture, Edges float64
	// HashDistance is the number of differing bits (0–64); a few means the same shot.
	HashDistance int
}

func Compare(a, b Signature) Score {
	s := Score{
		Color:        intersection(a.Color[:], b.Color[:]),
		Texture:      intersection(a.Texture[:], b.Texture[:]),
		Edges:        intersection(a.Edges[:], b.Edges[:]),
		HashDistance: bits.OnesCount64(a.Hash ^ b.Hash),
	}
	// Texture separates surfaces best; color and edge direction confirm.
	s.Overall = 0.45*s.Texture + 0.3*s.Color + 0.25*s.Edges
	return s
}

// Duplicate reports a near-identical shot (same photo sent twice, or a burst).
func (s Score) Duplicate() bool { return s.HashDistance <= 6 && s.Overall >= 0.9 }

func (s Signature) MarshalBinary() ([]byte, error) {
	buf := make([]byte, 0, signatureN)
	buf = binary.BigEndian.AppendUint64(buf, s.Hash)
	for _, part := range [][]float32{s.Color[:], s.Edges[:], s.Texture[:]} {
		for _, v := range part {
			buf = binary.BigEndian.AppendUint32(buf, math.Float32bits(v))
		}
	}
	return buf, nil
}

func (s *Signature) UnmarshalBinary(data []byte) error {
	if len(data) != signatureN {
		return errors.New("invalid signature length")
	}
	s.Hash = binary.BigEndian.Uint64(data)
	data = data[8:]
	for _, part := range [][]float32{s.Color[:], s.Edges[:], s.Texture[:]} {
		for i := range part {
			part[i] = math.Float32frombits(binary.BigEndian.Uint32(data))
			data = data[4:]
		}
	}
	return nil
}

// shrink averages the image into w×h cells (area sampling), channels in [0, 1].
func shrink(img image.Image, w, h int) (r, g, b []float64) {
	r, g, b = make([]float64, w*h), make([]float64, w*h), make([]float64, w*h)
	n := make([]float64, w*h)
	bounds := img.Bounds()
	dx, dy := bounds.Dx(), bounds.Dy()
	// Sampling every pixel of a 12 MP photo is wasteful; a stride keeps it under ~1 M reads.
	step := max(1, int(math.Sqrt(float64(dx*dy)/1e6)))
	for y := 0; y < dy; y += step {
		cy := y * h / dy
		for x := 0; x < dx; x += step {
			cr, cg, cb, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			i := cy*w + x*w/dx
			r[i] += float64(cr) / 65535
			g[i] += float64(cg) / 65535
			b[i] += float64(cb) / 65535
			n[i]++
		}
	}
	for i := range n {
		if n[i] > 0 {
			r[i], g[i], b[i] = r[i]/n[i], g[i]/n[i], b[i]/n[i]
		}
	}
	return r, g, b
}

func hsv(r, g, b float64) (h, s, v float64) {
	mx, mn := max(r, g, b), min(r, g, b)
	v, d := mx, mx-mn
	if mx > 0 {
		s = d / mx
	}
	switch {
	case d == 0:
		h = 0
	case mx == r:
		h = math.Mod((g-b)/d, 6) * 60
	case mx == g:
		h = ((b-r)/d + 2) * 60
	default:
		h = ((r-g)/d + 4) * 60
	}
	if h < 0 {
		h += 360
	}
	return h, s, v
}

// edges is a histogram of gradient directions weighted by strength (Sobel).
func edges(gray []float64, out *[edgeBins]float32) {
	at := func(x, y int) float64 { return gray[y*side+x] }
	for y := 1; y < side-1; y++ {
		for x := 1; x < side-1; x++ {
			gx := at(x+1, y-1) + 2*at(x+1, y) + at(x+1, y+1) - at(x-1, y-1) - 2*at(x-1, y) - at(x-1, y+1)
			gy := at(x-1, y+1) + 2*at(x, y+1) + at(x+1, y+1) - at(x-1, y-1) - 2*at(x, y-1) - at(x+1, y-1)
			mag := math.Hypot(gx, gy)
			if mag < 0.08 { // flat areas carry no direction
				continue
			}
			angle := math.Atan2(gy, gx) // direction modulo 180°: a line has no sign
			if angle < 0 {
				angle += math.Pi
			}
			out[min(int(angle/math.Pi*edgeBins), edgeBins-1)] += float32(mag)
		}
	}
	normalize(out[:])
}

// texture is a histogram of uniform local binary patterns: each pixel compared
// with its 8 neighbors, which captures cracks, tile joints and grain.
func texture(gray []float64, out *[lbpBins]float32) {
	offsets := [8][2]int{{-1, -1}, {0, -1}, {1, -1}, {1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}}
	for y := 1; y < side-1; y++ {
		for x := 1; x < side-1; x++ {
			c := gray[y*side+x]
			var code uint8
			for i, o := range offsets {
				if gray[(y+o[1])*side+x+o[0]] >= c+0.01 {
					code |= 1 << i
				}
			}
			// Uniform patterns have at most two 0/1 transitions around the circle.
			if bits.OnesCount8(code^bits.RotateLeft8(code, 1)) <= 2 {
				out[bits.OnesCount8(code)]++
			} else {
				out[lbpBins-1]++
			}
		}
	}
	normalize(out[:])
}

// dhash compares neighboring cells of a 9×8 grayscale thumbnail.
func dhash(img image.Image) uint64 {
	r, g, b := shrink(img, 9, 8)
	var hash uint64
	for y := range 8 {
		for x := range 8 {
			i := y*9 + x
			if 0.299*r[i]+0.587*g[i]+0.114*b[i] > 0.299*r[i+1]+0.587*g[i+1]+0.114*b[i+1] {
				hash |= 1 << (y*8 + x)
			}
		}
	}
	return hash
}

func normalize(v []float32) {
	var sum float32
	for _, x := range v {
		sum += x
	}
	if sum == 0 {
		return
	}
	for i := range v {
		v[i] /= sum
	}
}

func intersection(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(min(a[i], b[i]))
	}
	return s
}
