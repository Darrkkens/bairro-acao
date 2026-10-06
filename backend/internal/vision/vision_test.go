package vision

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func labelEmbedding(t *testing.T, g *Gate, text string) []float32 {
	t.Helper()
	for _, l := range g.labels {
		if l.Text == text {
			return l.Embedding
		}
	}
	t.Fatalf("no label %q", text)
	return nil
}

// A photo that looks exactly like a description is judged by that description.
func TestGateUsesTheClosestDescriptions(t *testing.T) {
	g, err := NewGate()
	if err != nil {
		t.Fatal(err)
	}
	if v := g.Judge(labelEmbedding(t, g, "a photo of a pothole in an asphalt road")); v.Problem < 0.9 || v.Looks != "buraco no asfalto" || v.Category != "via_publica" {
		t.Errorf("pothole: %+v", v)
	}
	if v := g.Judge(labelEmbedding(t, g, "a default user profile avatar icon")); v.Problem > MinProblem || v.Looks != "um avatar ou ícone" || v.Category != "" {
		t.Errorf("avatar: %+v", v)
	}
	for _, l := range g.labels {
		if len(l.Embedding) != Dims || l.PT == "" {
			t.Errorf("label %q: %d dims, pt %q", l.Text, len(l.Embedding), l.PT)
		}
	}
}

func TestPreprocessCropsTheCenterAndNormalizes(t *testing.T) {
	// 600×300: a black left third, white middle third, black right third.
	img := image.NewRGBA(image.Rect(0, 0, 600, 300))
	for y := range 300 {
		for x := range 600 {
			c := color.RGBA{A: 255}
			if x >= 200 && x < 400 {
				c = color.RGBA{255, 255, 255, 255}
			}
			img.Set(x, y, c)
		}
	}
	px, err := preprocess(img)
	if err != nil || len(px) != 3*side*side {
		t.Fatalf("got %d values, %v", len(px), err)
	}
	// The 224-wide center crop of the 448-wide resize spans x 112–336 of 600 → 150–450:
	// white in the middle, black at both edges.
	white, black := (1-mean[0])/std[0], (0-mean[0])/std[0]
	center, edge := px[112*side+112], px[112*side+2]
	if math.Abs(float64(center-white)) > 0.05 || math.Abs(float64(edge-black)) > 0.05 {
		t.Errorf("center %.2f (want %.2f), edge %.2f (want %.2f)", center, white, edge, black)
	}
	if _, err := preprocess(image.NewRGBA(image.Rect(0, 0, 10, 10))); err == nil {
		t.Error("tiny image accepted")
	}
}

func modelDir(t *testing.T) string {
	dir := os.Getenv("VISION_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "data", "vision")
	}
	if _, err := os.Stat(filepath.Join(dir, ModelFile)); err != nil {
		t.Skip("CLIP model not downloaded (scripts/baixar-modelo-visao.sh)")
	}
	return dir
}

func TestInspectorWithTheRealModel(t *testing.T) {
	inspector, err := OpenDir(modelDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer inspector.Close()
	avatar, err := os.ReadFile(filepath.Join("testdata", "avatar.png"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := inspector.Inspect(avatar)
	if err != nil {
		t.Fatal(err)
	}
	var norm float64
	for _, x := range got.Embedding {
		norm += float64(x) * float64(x)
	}
	if got.Relevant || math.Abs(norm-1) > 1e-3 {
		t.Fatalf("a profile placeholder must be discarded: %+v (norm %.3f)", got, norm)
	}
}

// TestEvaluate prints the gate's verdict for folders of photos, to re-check
// thresholds after changing the model, labels or preprocessing:
//
//	EVAL="keep=/path/problems,discard=/path/offtopic" go test -run TestEvaluate -v ./internal/vision
func TestEvaluate(t *testing.T) {
	sets := os.Getenv("EVAL")
	if sets == "" {
		t.Skip("set EVAL to evaluate folders of photos")
	}
	inspector, err := OpenDir(modelDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer inspector.Close()
	for _, set := range strings.Split(sets, ",") {
		name, dir, _ := strings.Cut(set, "=")
		files, _ := filepath.Glob(filepath.Join(dir, "*"))
		kept := 0
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			v, err := inspector.Inspect(data)
			if err != nil {
				continue
			}
			if v.Relevant {
				kept++
			}
			fmt.Printf("%-8s %-40s kept=%-5v problem=%.3f looks=%s\n", name, filepath.Base(f), v.Relevant, v.Problem, v.Looks)
		}
		fmt.Printf("%s: kept %d of %d\n", name, kept, len(files))
	}
}
