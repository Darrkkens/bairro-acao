// Package report turns a finished walk into a shareable, self-contained HTML
// file: photos are embedded, so it opens offline and can be sent as-is.
package report

import (
	"bairroacao/internal/photos"
	"bairroacao/internal/walk"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type Section struct {
	walk.CategoryInfo
	Items []Item
}

// Item is a confirmed point with its number on the map and its extra photos.
type Item struct {
	walk.Occurrence
	Number int
	Extra  []walk.Occurrence
}

type Report struct {
	Walk        walk.Walk
	GeneratedAt time.Time
	Recorded    int
	Confirmed   int
	Sections    []Section
	// How often the person kept the AI's category, to validate the model with real photos.
	AIMatched, AICompared int
	Models                []string
	// Route is the GPS track merged with photo locations; Distance is its length in meters.
	Route    []walk.TrackPoint
	Distance float64
	Map      *MapView
}

// Build keeps only occurrences the person reviewed, grouped in category order
// and numbered in the order they were photographed.
func Build(w walk.Walk, occurrences []walk.Occurrence, track []walk.TrackPoint, now time.Time) Report {
	r := Report{Walk: w, GeneratedAt: now}
	r.Route = walk.Route(track, occurrences)
	extras := map[string][]walk.Occurrence{}
	for _, o := range occurrences {
		if o.GroupID != "" {
			extras[o.GroupID] = append(extras[o.GroupID], o)
		} else {
			r.Recorded++
		}
	}
	r.Distance = walk.RouteLength(r.Route)
	byCategory := map[walk.Category][]Item{}
	models := map[string]bool{}
	ordered := slices.Clone(occurrences)
	slices.SortStableFunc(ordered, func(a, b walk.Occurrence) int { return a.CapturedAt.Compare(b.CapturedAt) })
	for _, o := range ordered {
		if o.ReviewedAt == nil || o.GroupID != "" {
			continue
		}
		r.Confirmed++
		byCategory[o.Category] = append(byCategory[o.Category], Item{o, r.Confirmed, extras[o.ID]})
		if o.AI != nil {
			r.AICompared++
			if o.AI.Category == o.Category {
				r.AIMatched++
			}
			if !models[o.AI.Model] {
				models[o.AI.Model] = true
				r.Models = append(r.Models, o.AI.Model)
			}
		}
	}
	for _, c := range walk.Categories {
		if items := byCategory[c.ID]; len(items) > 0 {
			r.Sections = append(r.Sections, Section{c, items})
		}
	}
	return r
}

// Filename is ASCII-only so every phone and mail client keeps it intact.
func Filename(r Report, loc *time.Location) string {
	var b strings.Builder
	for _, c := range norm.NFD.String(strings.ToLower(r.Walk.Neighborhood)) {
		switch {
		case c < unicode.MaxASCII && (unicode.IsLetter(c) || unicode.IsDigit(c)):
			b.WriteRune(c)
		case unicode.IsSpace(c) || c == '-':
			if s := b.String(); s != "" && !strings.HasSuffix(s, "-") {
				b.WriteByte('-')
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "bairro"
	}
	return fmt.Sprintf("bairro-em-acao-%s-%s.html", slug, r.Walk.StartedAt.In(loc).Format("2006-01-02"))
}

var months = [...]string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}

func longDate(t time.Time) string {
	return fmt.Sprintf("%d de %s de %d", t.Day(), months[t.Month()-1], t.Year())
}

func distance(meters float64) string {
	if meters < 1000 {
		return fmt.Sprintf("%.0f m", math.Round(meters/10)*10)
	}
	return strings.Replace(fmt.Sprintf("%.1f km", meters/1000), ".", ",", 1)
}

func duration(w walk.Walk) string {
	if w.FinishedAt == nil {
		return ""
	}
	d := w.FinishedAt.Sub(w.StartedAt).Round(time.Minute)
	if h := int(d.Hours()); h > 0 {
		return fmt.Sprintf("%d h %02d min", h, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d min", int(d.Minutes()))
}

//go:embed report.html.tmpl
var source string

// WriteHTML renders the report with every photo inlined as a data URI.
func WriteHTML(out io.Writer, r Report, store interface{ Read(string) ([]byte, error) }, loc *time.Location) error {
	images := map[string]template.URL{}
	for _, s := range r.Sections {
		for _, item := range s.Items {
			for _, o := range append([]walk.Occurrence{item.Occurrence}, item.Extra...) {
				data, err := store.Read(o.Photo)
				if err != nil {
					continue // the card renders without the image
				}
				images[o.Photo] = template.URL("data:" + photos.ContentType(o.Photo) + ";base64," + base64.StdEncoding.EncodeToString(data))
			}
		}
	}
	tmpl, err := template.New("report").Funcs(template.FuncMap{
		"date":     func(t time.Time) string { return longDate(t.In(loc)) },
		"clock":    func(t time.Time) string { return t.In(loc).Format("15:04") },
		"duration": duration,
		"distance": distance,
		"photo":    func(name string) template.URL { return images[name] },
		"mapURL": func(l walk.Location) string {
			return fmt.Sprintf("https://www.openstreetmap.org/?mlat=%.6f&mlon=%.6f#map=19/%.6f/%.6f", l.Latitude, l.Longitude, l.Latitude, l.Longitude)
		},
		"coords":  func(l walk.Location) string { return fmt.Sprintf("%.5f, %.5f", l.Latitude, l.Longitude) },
		"meters":  func(f float64) string { return fmt.Sprintf("±%.0f m", f) },
		"percent": func(a, b int) int { return a * 100 / b },
		"plural": func(n int, one, many string) string {
			if n == 1 {
				return one
			}
			return many
		},
		"join": strings.Join,
	}).Parse(source)
	if err != nil {
		return err
	}
	return tmpl.Execute(out, r)
}
