// Package walk models a neighborhood walk: the walk itself, the occurrences
// recorded along the way and the fixed list of problem categories.
package walk

import (
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrNotFound = errors.New("Registro não encontrado.")

// ValidationError carries a message meant for the person using the app.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalid(message string) error { return &ValidationError{message} }

type Category string

const (
	Cleaning  Category = "limpeza"
	Sidewalks Category = "calcadas"
	Street    Category = "via_publica"
	Leisure   Category = "lazer"
	Other     Category = "outros"
)

type CategoryInfo struct {
	ID       Category `json:"id"`
	Label    string   `json:"label"`
	Examples string   `json:"examples"`
}

// Categories is the fixed list shown to the AI and to the person reviewing.
// frontend/src/categories.ts mirrors it so the app works offline.
var Categories = []CategoryInfo{
	{Cleaning, "Limpeza", "Lixo acumulado, descarte irregular"},
	{Sidewalks, "Calçadas e acessibilidade", "Piso danificado, passagem obstruída"},
	{Street, "Via pública", "Buraco, sinalização danificada"},
	{Leisure, "Espaços de lazer", "Banco quebrado, equipamento deteriorado"},
	{Other, "Outros", "Situações que precisam de descrição manual"},
}

func (c Category) Info() (CategoryInfo, bool) {
	for _, info := range Categories {
		if info.ID == c {
			return info, true
		}
	}
	return CategoryInfo{}, false
}

type Walk struct {
	ID           string `json:"id"`
	Neighborhood string `json:"neighborhood"`
	City         string `json:"city"`
	// State is the two-letter UF code, e.g. "SP".
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// States are the 27 Brazilian federative units.
var States = []string{"AC", "AL", "AM", "AP", "BA", "CE", "DF", "ES", "GO", "MA", "MG", "MS", "MT", "PA", "PB", "PE", "PI", "PR", "RJ", "RN", "RO", "RR", "RS", "SC", "SE", "SP", "TO"}

// TrackPoint is one GPS fix recorded by the phone during the walk.
type TrackPoint struct {
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	Accuracy   *float64  `json:"accuracy_m,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// MaxTrackPoints bounds one walk's route; the app thins longer tracks before sending.
const MaxTrackPoints = 5000

type WalkSummary struct {
	Walk
	Occurrences int `json:"occurrences"`
	Reviewed    int `json:"reviewed"`
}

type AIStatus string

const (
	AIPending   AIStatus = "pending"
	AIRunning   AIStatus = "running"
	AIDone      AIStatus = "done"
	AINeedsInfo AIStatus = "needs_info" // the photo was not enough; Suggestion.Question says what to add
	AIFailed    AIStatus = "failed"
	// AIGrouped marks an extra photo of a point: the point's analysis covers it.
	AIGrouped AIStatus = "grouped"
)

// Suggestion is the AI's reading of one occurrence, kept apart from what the
// person confirmed so the two can be compared when validating the model.
type Suggestion struct {
	Category    Category `json:"category"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Confidence  string   `json:"confidence"`
	Question    string   `json:"question,omitempty"`
	Model       string   `json:"model"`
}

type Location struct {
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Accuracy  *float64 `json:"accuracy_m,omitempty"`
}

type Occurrence struct {
	ID         string      `json:"id"`
	WalkID     string      `json:"walk_id"`
	Photo      string      `json:"photo"`
	Note       string      `json:"note"`
	Location   *Location   `json:"location"`
	CapturedAt time.Time   `json:"captured_at"`
	AIStatus   AIStatus    `json:"ai_status"`
	AI         *Suggestion `json:"ai"`
	AIError    string      `json:"ai_error,omitempty"`
	// Confirmed by the person; empty until reviewed.
	Category    Category   `json:"category"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	ReviewedAt  *time.Time `json:"reviewed_at"`
	// GroupID is set on an extra photo of a point and names the point it belongs to.
	GroupID string `json:"group_id,omitempty"`
	// Duplicate marks a near-identical shot grouped automatically before analysis.
	Duplicate bool `json:"duplicate,omitempty"`
	// SuggestedGroup is computed on read: an earlier point this one probably shows again.
	SuggestedGroup *GroupSuggestion `json:"suggested_group,omitempty"`
	// KeepSeparate records that the person said this is a different problem.
	KeepSeparate bool `json:"-"`
	// Signature describes the photo for duplicate and similarity checks (internal/similarity).
	Signature []byte `json:"-"`
	// Embedding is the photo's CLIP vector (internal/vision), when the model is set up.
	Embedding []float32 `json:"-"`
}

// Inspection is what the image model says about an upload before Gemma sees it.
type Inspection struct {
	Embedding []float32
	// Relevant is false for photos that show nothing the app reports (a car, a profile picture).
	Relevant bool
	Problem  float64
	// Looks is the closest description in Portuguese, e.g. "um carro".
	Looks string
}

// DiscardedError rejects an upload that shows nothing the app reports. The
// photo is not stored and never reaches Gemma.
type DiscardedError struct {
	Looks   string
	Problem float64
}

func (e *DiscardedError) Error() string {
	return "Foto descartada: parece " + e.Looks + ", não um problema em espaço público."
}

// GroupSuggestion explains why two points look like the same problem.
type GroupSuggestion struct {
	ID         string  `json:"id"`
	DistanceM  float64 `json:"distance_m"`
	Seconds    float64 `json:"seconds"`
	Similarity float64 `json:"similarity"`
}

// Review is the person's confirmation or correction of an occurrence.
type Review struct {
	Category    Category `json:"category"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// NormalizeID accepts the UUIDs the app generates offline, in canonical lower case.
func NormalizeID(id string) (string, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	return id, idPattern.MatchString(id)
}

// cleanText collapses runs of spaces and tabs, keeps line breaks, and enforces a rune limit.
func cleanText(s string, max int) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	s = strings.TrimSpace(strings.Join(lines, "\n"))
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s, utf8.ValidString(s) && utf8.RuneCountInString(s) <= max
}

func NormalizeNeighborhood(s string) (string, error) {
	s, ok := cleanText(strings.ReplaceAll(s, "\n", " "), 80)
	if !ok || utf8.RuneCountInString(s) < 2 {
		return "", invalid("Informe o nome do bairro (de 2 a 80 caracteres).")
	}
	return s, nil
}

func NormalizeCity(s string) (string, error) {
	s, ok := cleanText(strings.ReplaceAll(s, "\n", " "), 80)
	if !ok || utf8.RuneCountInString(s) < 2 {
		return "", invalid("Informe a cidade (de 2 a 80 caracteres).")
	}
	return s, nil
}

func NormalizeState(s string) (string, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	for _, uf := range States {
		if s == uf {
			return s, nil
		}
	}
	return "", invalid("Escolha o estado (UF).")
}

// CleanTrack keeps valid fixes in time order. A bad point is dropped rather
// than rejecting the walk: the track is a nice-to-have, the finish is not.
func CleanTrack(points []TrackPoint, now time.Time) []TrackPoint {
	clean := make([]TrackPoint, 0, len(points))
	for _, p := range points {
		if ValidateLocation(&Location{Latitude: p.Latitude, Longitude: p.Longitude, Accuracy: p.Accuracy}) != nil || ValidateTime(p.RecordedAt, now) != nil {
			continue
		}
		p.RecordedAt = p.RecordedAt.UTC()
		clean = append(clean, p)
		if len(clean) == MaxTrackPoints {
			break
		}
	}
	slices.SortFunc(clean, func(a, b TrackPoint) int { return a.RecordedAt.Compare(b.RecordedAt) })
	return clean
}

func NormalizeNote(s string) (string, error) {
	s, ok := cleanText(s, 1000)
	if !ok {
		return "", invalid("A observação pode ter no máximo 1000 caracteres.")
	}
	return s, nil
}

func ValidateLocation(l *Location) error {
	if l == nil {
		return nil
	}
	if math.IsNaN(l.Latitude) || math.IsNaN(l.Longitude) || l.Latitude < -90 || l.Latitude > 90 || l.Longitude < -180 || l.Longitude > 180 {
		return invalid("Localização inválida.")
	}
	if l.Accuracy != nil && (math.IsNaN(*l.Accuracy) || *l.Accuracy < 0 || *l.Accuracy > 100000) {
		return invalid("Precisão da localização inválida.")
	}
	return nil
}

// ValidateTime accepts dates from the phone, which may sync days later, but
// rejects clocks running far ahead.
func ValidateTime(t, now time.Time) error {
	if t.IsZero() || t.After(now.Add(24*time.Hour)) || t.Before(now.AddDate(-1, 0, 0)) {
		return invalid("Data e hora do registro inválidas.")
	}
	return nil
}

func NormalizeReview(r Review) (Review, error) {
	if _, ok := r.Category.Info(); !ok {
		return Review{}, invalid("Escolha uma das categorias.")
	}
	title, ok := cleanText(strings.ReplaceAll(r.Title, "\n", " "), 80)
	if !ok || title == "" {
		return Review{}, invalid("Informe um título de até 80 caracteres.")
	}
	description, ok := cleanText(r.Description, 1000)
	if !ok {
		return Review{}, invalid("A descrição pode ter no máximo 1000 caracteres.")
	}
	return Review{r.Category, title, description}, nil
}

// PhotoKind returns the file extension for the image formats the AI accepts.
func PhotoKind(data []byte) (string, bool) {
	switch {
	case len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "jpg", true
	case len(data) > 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "png", true
	}
	return "", false
}

// Distance is the great-circle distance in meters between two coordinates.
func Distance(lat1, lng1, lat2, lng2 float64) float64 {
	const earth = 6371000.0
	rad := math.Pi / 180
	dLat, dLng := (lat2-lat1)*rad, (lng2-lng1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earth * math.Asin(math.Min(1, math.Sqrt(a)))
}

// Route merges the GPS track with the places where photos were taken, in time
// order. Without a track (screen off, permission denied) the photo locations
// alone still sketch the walk.
func Route(track []TrackPoint, occurrences []Occurrence) []TrackPoint {
	route := slices.Clone(track)
	for _, o := range occurrences {
		if o.Location != nil {
			route = append(route, TrackPoint{Latitude: o.Location.Latitude, Longitude: o.Location.Longitude, Accuracy: o.Location.Accuracy, RecordedAt: o.CapturedAt})
		}
	}
	slices.SortStableFunc(route, func(a, b TrackPoint) int { return a.RecordedAt.Compare(b.RecordedAt) })
	return route
}

func RouteLength(route []TrackPoint) float64 {
	total := 0.0
	for i := 1; i < len(route); i++ {
		total += Distance(route[i-1].Latitude, route[i-1].Longitude, route[i].Latitude, route[i].Longitude)
	}
	return total
}
