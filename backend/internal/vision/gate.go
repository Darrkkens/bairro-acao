package vision

import (
	_ "embed"
	"encoding/json"
	"math"
)

// labels.json holds CLIP text embeddings of short descriptions: problems the
// app reports (tagged with their category) and things it does not ("fora").
// Regenerate with scripts/clip-labels.mjs when the model or texts change.
//
//go:embed labels.json
var labelsJSON []byte

const offTopic = "fora"

// MinProblem is the probability below which a photo is discarded. Measured on
// 137 problem photos and 17 off-topic ones: problems scored ≥ 0.236 (except
// two indoor classroom shots), clear off-topic photos ≤ 0.102 (avatar 0.014).
// Street scenes without a visible problem score high and are kept on purpose:
// discarding deletes the photo, so doubt goes to the person and to Gemma.
const MinProblem = 0.2

type Label struct {
	Category  string    `json:"category"`
	Text      string    `json:"text"`
	PT        string    `json:"pt"`
	Embedding []float32 `json:"embedding"`
}

type Gate struct{ labels []Label }

func NewGate() (*Gate, error) {
	var file struct {
		Labels []Label `json:"labels"`
	}
	if err := json.Unmarshal(labelsJSON, &file); err != nil {
		return nil, err
	}
	return &Gate{labels: file.Labels}, nil
}

// Verdict explains why a photo was kept or discarded.
type Verdict struct {
	// Problem is the zero-shot probability that the photo shows something the app reports.
	Problem float64
	// Looks is the closest description, in Portuguese ("um carro", "calçada quebrada").
	Looks    string
	Category string // set when the closest description is a problem
}

// CLIP compares images and texts with a learned temperature of 100.
const logitScale = 100

// Judge is zero-shot classification: a softmax over every description,
// summing the probability of the ones the app reports.
func (g *Gate) Judge(e []float32) Verdict {
	logits := make([]float64, len(g.labels))
	best := 0
	for i, l := range g.labels {
		logits[i] = logitScale * Cosine(e, l.Embedding)
		if logits[i] > logits[best] {
			best = i
		}
	}
	var sum, problem float64
	for i, l := range g.labels {
		p := math.Exp(logits[i] - logits[best])
		sum += p
		if l.Category != offTopic {
			problem += p
		}
	}
	v := Verdict{Problem: problem / sum, Looks: g.labels[best].PT}
	if g.labels[best].Category != offTopic {
		v.Category = g.labels[best].Category
	}
	return v
}
