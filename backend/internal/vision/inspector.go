package vision

import (
	"bairroacao/internal/walk"
	"errors"
	"os"
	"path/filepath"
)

// Files expected in the vision directory (scripts/baixar-modelo-visao.sh downloads them).
const (
	LibraryFile = "onnxruntime-linux-x64-1.29.0/lib/libonnxruntime.so"
	ModelFile   = "clip-vision-q8.onnx" // openai/clip-vit-base-patch32, int8-quantized ONNX
)

// Inspector embeds and judges uploads for walk.Service.
type Inspector struct {
	enc  *Encoder
	gate *Gate
}

// OpenDir loads ONNX Runtime and the CLIP model from dir; a missing file means
// the feature is not set up, which callers treat as "run without it".
func OpenDir(dir string) (*Inspector, error) {
	lib, model := filepath.Join(dir, LibraryFile), filepath.Join(dir, ModelFile)
	for _, f := range []string{lib, model} {
		if _, err := os.Stat(f); err != nil {
			return nil, errors.New("missing " + f + "; run scripts/baixar-modelo-visao.sh")
		}
	}
	enc, err := Open(lib, model)
	if err != nil {
		return nil, err
	}
	gate, err := NewGate()
	if err != nil {
		enc.Close()
		return nil, err
	}
	return &Inspector{enc: enc, gate: gate}, nil
}

func (i *Inspector) Close() { i.enc.Close() }

func (i *Inspector) Inspect(photo []byte) (walk.Inspection, error) {
	e, err := i.enc.Embed(photo)
	if err != nil {
		return walk.Inspection{}, err
	}
	v := i.gate.Judge(e)
	return walk.Inspection{Embedding: e, Relevant: v.Problem >= MinProblem, Problem: v.Problem, Looks: v.Looks}, nil
}
