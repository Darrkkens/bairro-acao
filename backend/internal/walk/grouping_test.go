package walk

import (
	"bairroacao/internal/similarity"
	"math"
	"testing"
	"time"
)

// sig gives occurrences signatures with a chosen similarity: equal names are identical photos.
func sig(t *testing.T, name string) []byte {
	t.Helper()
	var s similarity.Signature
	switch name {
	case "sidewalk":
		s.Texture[0], s.Color[0], s.Edges[0] = 1, 1, 1
	case "sidewalk-other-angle": // ~0.88: same surface, different framing
		s.Texture[0], s.Texture[1] = 0.85, 0.15
		s.Color[0], s.Color[1] = 0.9, 0.1
		s.Edges[0], s.Edges[1] = 0.9, 0.1
		s.Hash = 0xFFFF_FFFF
	case "asphalt": // ~0.6 against the sidewalk
		s.Texture[0], s.Texture[2] = 0.6, 0.4
		s.Color[0], s.Color[2] = 0.6, 0.4
		s.Edges[0], s.Edges[2] = 0.6, 0.4
		s.Hash = 0xFF00_FF00_FF00
	}
	raw, _ := s.MarshalBinary()
	return raw
}

func at(lat, lng, accuracy float64) *Location {
	return &Location{Latitude: lat, Longitude: lng, Accuracy: &accuracy}
}

func TestSuggestGroups(t *testing.T) {
	t0 := time.Date(2026, 10, 5, 20, 16, 0, 0, time.UTC)
	calcadas := &Suggestion{Category: Sidewalks}
	occurrences := []Occurrence{
		{ID: "a", CapturedAt: t0, AI: calcadas, Location: at(-27.16776, -51.51792, 23), Signature: sig(t, "sidewalk")},
		{ID: "b", CapturedAt: t0.Add(13 * time.Second), AI: calcadas, Location: at(-27.16773, -51.51782, 24), Signature: sig(t, "sidewalk-other-angle")},
		// Same place and minute but a different surface: not suggested.
		{ID: "c", CapturedAt: t0.Add(20 * time.Second), AI: calcadas, Location: at(-27.16774, -51.51785, 20), Signature: sig(t, "asphalt")},
		// Same photo, other category (the person corrected it): not suggested.
		{ID: "d", CapturedAt: t0.Add(25 * time.Second), AI: &Suggestion{Category: Street}, Location: at(-27.16776, -51.51792, 20), Signature: sig(t, "sidewalk")},
		// Same photo, three minutes later: not suggested.
		{ID: "e", CapturedAt: t0.Add(3 * time.Minute), AI: calcadas, Location: at(-27.16776, -51.51792, 20), Signature: sig(t, "sidewalk")},
		// Same photo, 80 m away: not suggested.
		{ID: "f", CapturedAt: t0.Add(30 * time.Second), AI: calcadas, Location: at(-27.16706, -51.51792, 20), Signature: sig(t, "sidewalk")},
		// The person already said it is different: not suggested again.
		{ID: "g", CapturedAt: t0.Add(35 * time.Second), AI: calcadas, Location: at(-27.16776, -51.51792, 20), Signature: sig(t, "sidewalk"), KeepSeparate: true},
	}
	suggestGroups(occurrences)
	if s := occurrences[1].SuggestedGroup; s == nil || s.ID != "a" || s.DistanceM > 15 || s.Seconds != 13 {
		t.Fatalf("b should be suggested into a, got %+v", s)
	}
	for _, o := range occurrences[2:] {
		if o.SuggestedGroup != nil {
			t.Errorf("%s: unexpected suggestion %+v", o.ID, o.SuggestedGroup)
		}
	}
}

func TestDuplicateOfIgnoresExtraPhotosAndOldShots(t *testing.T) {
	t0 := time.Now()
	var photo similarity.Signature
	_ = photo.UnmarshalBinary(sig(t, "sidewalk"))
	existing := []Occurrence{
		{ID: "old", CapturedAt: t0.Add(-time.Hour), Signature: sig(t, "sidewalk")},
		{ID: "extra", GroupID: "x", CapturedAt: t0, Signature: sig(t, "sidewalk")},
		{ID: "point", CapturedAt: t0.Add(-time.Minute), Signature: sig(t, "sidewalk")},
	}
	if id, ok := duplicateOf(photo, t0, existing); !ok || id != "point" {
		t.Fatalf("got %q %v", id, ok)
	}
}

func TestSameSceneNeedsTimePlaceAndBothSimilarities(t *testing.T) {
	t0 := time.Date(2026, 10, 5, 20, 16, 0, 0, time.UTC)
	unit := func(x float32) []float32 {
		v := make([]float32, 4)
		v[0] = x
		v[1] = float32(math.Sqrt(float64(1 - x*x)))
		return v
	}
	sidewalk := Occurrence{ID: "a", CapturedAt: t0, Location: at(-27.16776, -51.51792, 23), Signature: sig(t, "sidewalk"), Embedding: unit(1)}
	extra := Occurrence{ID: "x", GroupID: "a", CapturedAt: t0.Add(5 * time.Second), Location: at(-27.16776, -51.51792, 23), Signature: sig(t, "sidewalk"), Embedding: unit(1)}
	cases := []struct {
		name string
		o    Occurrence
		want string
	}{
		{"another shot of the same sidewalk", Occurrence{CapturedAt: t0.Add(13 * time.Second), Location: at(-27.16773, -51.51782, 24), Signature: sig(t, "sidewalk-other-angle"), Embedding: unit(0.9)}, "a"},
		{"CLIP says a different scene", Occurrence{CapturedAt: t0.Add(13 * time.Second), Location: at(-27.16773, -51.51782, 24), Signature: sig(t, "sidewalk-other-angle"), Embedding: unit(0.76)}, ""},
		{"signature says a different surface", Occurrence{CapturedAt: t0.Add(13 * time.Second), Location: at(-27.16773, -51.51782, 24), Signature: sig(t, "asphalt"), Embedding: unit(0.95)}, ""},
		{"too far away", Occurrence{CapturedAt: t0.Add(13 * time.Second), Location: at(-27.16706, -51.51792, 10), Signature: sig(t, "sidewalk"), Embedding: unit(1)}, ""},
		{"too late", Occurrence{CapturedAt: t0.Add(3 * time.Minute), Location: at(-27.16776, -51.51792, 23), Signature: sig(t, "sidewalk"), Embedding: unit(1)}, ""},
		{"no location needs a near-identical quick repeat", Occurrence{CapturedAt: t0.Add(30 * time.Second), Signature: sig(t, "sidewalk"), Embedding: unit(0.99)}, "a"},
		{"no model, no automatic grouping", Occurrence{CapturedAt: t0.Add(13 * time.Second), Location: at(-27.16773, -51.51782, 24), Signature: sig(t, "sidewalk")}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := sameScene(c.o, []Occurrence{sidewalk, extra})
			if got != c.want || ok != (c.want != "") {
				t.Fatalf("got %q %v, want %q", got, ok, c.want)
			}
		})
	}
	// Matching an extra photo groups with its point, not with the extra.
	if got, _ := sameScene(Occurrence{CapturedAt: t0.Add(10 * time.Second), Location: at(-27.16776, -51.51792, 23), Signature: sig(t, "sidewalk"), Embedding: unit(1)}, []Occurrence{extra}); got != "a" {
		t.Errorf("got %q, want the point a", got)
	}
}
