// Package vision describes photos with CLIP, an open image model (OpenAI's
// ViT-B/32, MIT license) run locally through ONNX Runtime. A photo becomes a
// 512-number vector of what it shows: close vectors mean similar content.
//
// It runs before Gemma on every upload, in about a tenth of a second: photos
// that show nothing the app reports (a car, a house, a profile picture) are
// discarded, and repeated shots of one problem are grouped so Gemma reads the
// problem once.
package vision

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"runtime"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	side = 224 // CLIP ViT-B/32 input
	// Dims is the length of an embedding.
	Dims = 512
)

// CLIP's normalization constants (preprocessor_config.json).
var (
	mean = [3]float32{0.48145466, 0.4578275, 0.40821073}
	std  = [3]float32{0.26862954, 0.26130258, 0.27577711}
)

type Encoder struct {
	mu      sync.Mutex
	session *ort.AdvancedSession
	input   *ort.Tensor[float32]
	output  *ort.Tensor[float32]
}

var initOnce sync.Once
var initErr error

// Open loads ONNX Runtime from libPath and the CLIP vision model from modelPath.
func Open(libPath, modelPath string) (*Encoder, error) {
	initOnce.Do(func() {
		ort.SetSharedLibraryPath(libPath)
		initErr = ort.InitializeEnvironment()
	})
	if initErr != nil {
		return nil, fmt.Errorf("ONNX Runtime: %w", initErr)
	}
	input, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, side, side))
	if err != nil {
		return nil, err
	}
	output, err := ort.NewEmptyTensor[float32](ort.NewShape(1, Dims))
	if err != nil {
		input.Destroy()
		return nil, err
	}
	options, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer options.Destroy()
	_ = options.SetIntraOpNumThreads(max(1, runtime.NumCPU()/2))
	session, err := ort.NewAdvancedSession(modelPath, []string{"pixel_values"}, []string{"image_embeds"},
		[]ort.Value{input}, []ort.Value{output}, options)
	if err != nil {
		input.Destroy()
		output.Destroy()
		return nil, fmt.Errorf("CLIP model: %w", err)
	}
	return &Encoder{session: session, input: input, output: output}, nil
}

func (e *Encoder) Close() {
	e.session.Destroy()
	e.input.Destroy()
	e.output.Destroy()
}

// Embed returns the photo's unit-length CLIP embedding.
func (e *Encoder) Embed(photo []byte) ([]float32, error) {
	img, _, err := image.Decode(bytes.NewReader(photo))
	if err != nil {
		return nil, err
	}
	pixels, err := preprocess(img)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	copy(e.input.GetData(), pixels)
	if err := e.session.Run(); err != nil {
		return nil, err
	}
	out := make([]float32, Dims)
	copy(out, e.output.GetData())
	return normalize(out), nil
}

// preprocess resizes the shorter side to 224 with antialiased bicubic
// filtering, crops the center and normalizes: the same steps as CLIP's
// processor (PIL BICUBIC), so embeddings match the model's training.
func preprocess(img image.Image) ([]float32, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 32 || h < 32 {
		return nil, errors.New("image too small")
	}
	outW, outH := side, side
	if w < h {
		outH = int(math.Round(float64(h) * side / float64(w)))
	} else {
		outW = int(math.Round(float64(w) * side / float64(h)))
	}
	rgb := toFloatRGB(img)
	resized := resize(rgb, w, h, outW, outH)
	top, left := (outH-side)/2, (outW-side)/2
	out := make([]float32, 3*side*side)
	for y := range side {
		for x := range side {
			i := ((y+top)*outW + x + left) * 3
			for c := range 3 {
				v := math.Round(min(255, max(0, resized[i+c]))) / 255 // PIL works in 8-bit
				out[c*side*side+y*side+x] = (float32(v) - mean[c]) / std[c]
			}
		}
	}
	return out, nil
}

// toFloatRGB copies the image into an interleaved RGB array in 0–255.
func toFloatRGB(img image.Image) []float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]float64, w*h*3)
	for y := range h {
		for x := range w {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := (y*w + x) * 3
			out[i], out[i+1], out[i+2] = float64(r>>8), float64(g>>8), float64(bl>>8)
		}
	}
	return out
}

// cubic is the Keys kernel with a = -0.5, as in PIL.
func cubic(x float64) float64 {
	const a = -0.5
	x = math.Abs(x)
	switch {
	case x < 1:
		return ((a+2)*x-(a+3))*x*x + 1
	case x < 2:
		return (((x-5)*x+8)*x - 4) * a
	}
	return 0
}

// resize scales an RGB array with separable antialiased bicubic filtering:
// when shrinking, the kernel widens by the scale factor, as PIL does.
func resize(src []float64, w, h, outW, outH int) []float64 {
	horizontal := pass(src, w, h, outW, true)
	return pass(horizontal, outW, h, outH, false)
}

func pass(src []float64, w, h, out int, horizontal bool) []float64 {
	in, lines := w, h
	if !horizontal {
		in, lines = h, w
	}
	scale := float64(in) / float64(out)
	support := 2 * max(scale, 1)
	type tap struct {
		start   int
		weights []float64
	}
	taps := make([]tap, out)
	for o := range out {
		center := (float64(o) + 0.5) * scale
		lo, hi := max(int(center-support+0.5), 0), min(int(center+support+0.5), in)
		ws := make([]float64, hi-lo)
		var sum float64
		for i := range ws {
			ws[i] = cubic((float64(lo+i) - center + 0.5) / max(scale, 1))
			sum += ws[i]
		}
		for i := range ws {
			ws[i] /= sum
		}
		taps[o] = tap{lo, ws}
	}
	var dst []float64
	if horizontal {
		dst = make([]float64, out*h*3)
	} else {
		dst = make([]float64, w*out*3)
	}
	for line := range lines {
		for o, t := range taps {
			var r, g, b float64
			for k, wt := range t.weights {
				var i int
				if horizontal {
					i = (line*w + t.start + k) * 3
				} else {
					i = ((t.start+k)*w + line) * 3
				}
				r, g, b = r+wt*src[i], g+wt*src[i+1], b+wt*src[i+2]
			}
			var j int
			if horizontal {
				j = (line*out + o) * 3
			} else {
				j = (o*w + line) * 3
			}
			dst[j], dst[j+1], dst[j+2] = r, g, b
		}
	}
	return dst
}

func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
	return v
}

// Cosine is the similarity of two unit vectors, from -1 to 1.
func Cosine(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}
