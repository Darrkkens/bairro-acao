package walk

import (
	"bairroacao/internal/similarity"
	"math"
	"time"
)

// Grouping thresholds, calibrated on a real walk (Joaçaba/SC, 9 photos):
// three shots of one broken sidewalk scored 0.87–0.93, while two different
// asphalt problems 9 m and 22 s apart scored 0.84. Street surfaces all look
// alike to cheap descriptors, so similarity alone never groups points; it only
// backs up the same category, time and place, and the person always decides.
const (
	duplicateWindow    = 10 * time.Minute
	suggestWindow      = 2 * time.Minute
	suggestSimilarity  = 0.85
	minRadiusM         = 15.0
	maxRadiusM         = 40.0
	noLocationWindow   = time.Minute
	noLocationMinScore = 0.9
)

func signatureOf(o Occurrence) (similarity.Signature, bool) {
	var s similarity.Signature
	if len(o.Signature) == 0 || s.UnmarshalBinary(o.Signature) != nil {
		return s, false
	}
	return s, true
}

// duplicateOf finds an earlier point in the walk that the new photo repeats
// (a burst, or the same shot sent twice). Repeats skip the AI entirely.
func duplicateOf(sig similarity.Signature, capturedAt time.Time, existing []Occurrence) (string, bool) {
	for _, o := range existing {
		if o.GroupID != "" || absDuration(o.CapturedAt.Sub(capturedAt)) > duplicateWindow {
			continue
		}
		if other, ok := signatureOf(o); ok && similarity.Compare(sig, other).Duplicate() {
			return o.ID, true
		}
	}
	return "", false
}

// effectiveCategory is what the person confirmed, or else what the AI suggested.
func effectiveCategory(o Occurrence) Category {
	if o.ReviewedAt != nil && o.Category != "" {
		return o.Category
	}
	if o.AI != nil {
		return o.AI.Category
	}
	return ""
}

// suggestGroups marks points that probably show an earlier point again: same
// category, taken within two minutes, within GPS error of each other and with
// similar photos. occurrences must be in capture order.
func suggestGroups(occurrences []Occurrence) {
	for i := range occurrences {
		o := &occurrences[i]
		if o.GroupID != "" || o.KeepSeparate {
			continue
		}
		category := effectiveCategory(*o)
		sig, ok := signatureOf(*o)
		if category == "" || !ok {
			continue
		}
		for j := range i {
			p := occurrences[j]
			if p.GroupID != "" || effectiveCategory(p) != category {
				continue
			}
			gap := absDuration(o.CapturedAt.Sub(p.CapturedAt))
			other, ok := signatureOf(p)
			if !ok || gap > suggestWindow {
				continue
			}
			score := similarity.Compare(sig, other).Overall
			distance, located := pointDistance(*o, p)
			near := located && distance <= radius(*o, p) && score >= suggestSimilarity
			// Without location only a quick, very similar repeat counts.
			if !located {
				near = gap <= noLocationWindow && score >= noLocationMinScore
			}
			if near {
				o.SuggestedGroup = &GroupSuggestion{ID: p.ID, DistanceM: math.Round(distance), Seconds: math.Round(gap.Seconds()), Similarity: math.Round(score*100) / 100}
				break // the earliest match: later ones are usually already in its group
			}
		}
	}
}

func pointDistance(a, b Occurrence) (float64, bool) {
	if a.Location == nil || b.Location == nil {
		return 0, false
	}
	return Distance(a.Location.Latitude, a.Location.Longitude, b.Location.Latitude, b.Location.Longitude), true
}

// radius is how far apart two fixes of the same spot can be: the better of the
// two GPS accuracies, kept between 15 and 40 m.
func radius(a, b Occurrence) float64 {
	r := maxRadiusM
	for _, o := range []Occurrence{a, b} {
		if o.Location != nil && o.Location.Accuracy != nil {
			r = math.Min(r, *o.Location.Accuracy)
		}
	}
	return math.Max(minRadiusM, r)
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
