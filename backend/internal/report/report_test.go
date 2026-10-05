package report

import (
	"bairroacao/internal/walk"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

type photoStub map[string][]byte

func (p photoStub) Read(name string) ([]byte, error) {
	if data, ok := p[name]; ok {
		return data, nil
	}
	return nil, errors.New("missing")
}

func sample() (walk.Walk, []walk.Occurrence) {
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	end := start.Add(75 * time.Minute)
	reviewed := end
	w := walk.Walk{ID: "w", Neighborhood: "São João do Tauape", City: "Fortaleza", State: "CE", StartedAt: start, FinishedAt: &end}
	return w, []walk.Occurrence{
		{ID: "1", Photo: "a.jpg", Category: walk.Leisure, Title: "Banco quebrado", ReviewedAt: &reviewed, CapturedAt: start.Add(time.Minute), AI: &walk.Suggestion{Category: walk.Leisure, Model: "gemma3:4b"}},
		{ID: "2", Photo: "b.jpg", Category: walk.Street, Title: `<script>alert("x")</script>`, ReviewedAt: &reviewed, CapturedAt: start.Add(2 * time.Minute), AI: &walk.Suggestion{Category: walk.Other, Model: "gemma3:4b"},
			Location: &walk.Location{Latitude: -3.7319, Longitude: -38.5267}},
		{ID: "3", Photo: "c.jpg", CapturedAt: start, AIStatus: walk.AIPending},
	}
}

func TestBuildGroupsReviewedInCategoryOrder(t *testing.T) {
	w, occurrences := sample()
	r := Build(w, occurrences, nil, time.Now())
	if r.Recorded != 3 || r.Confirmed != 2 || len(r.Sections) != 2 {
		t.Fatalf("got %+v", r)
	}
	if r.Sections[0].ID != walk.Street || r.Sections[1].ID != walk.Leisure {
		t.Errorf("sections out of category order: %s, %s", r.Sections[0].ID, r.Sections[1].ID)
	}
	if r.AIMatched != 1 || r.AICompared != 2 || len(r.Models) != 1 {
		t.Errorf("AI agreement %d/%d models %v", r.AIMatched, r.AICompared, r.Models)
	}
	// Numbers follow the order the photos were taken, not the category order.
	if r.Sections[0].Items[0].Number != 2 || r.Sections[1].Items[0].Number != 1 {
		t.Errorf("numbers %d, %d", r.Sections[0].Items[0].Number, r.Sections[1].Items[0].Number)
	}
}

func TestFilenameIsASCII(t *testing.T) {
	w, occurrences := sample()
	got := Filename(Build(w, occurrences, nil, time.Now()), time.UTC)
	if got != "bairro-em-acao-sao-joao-do-tauape-2026-10-05.html" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteHTMLEmbedsPhotosAndEscapesText(t *testing.T) {
	w, occurrences := sample()
	track := []walk.TrackPoint{
		{Latitude: -3.7330, Longitude: -38.5280, RecordedAt: w.StartedAt},
		{Latitude: -3.7325, Longitude: -38.5272, RecordedAt: w.StartedAt.Add(90 * time.Second)},
	}
	r := Build(w, occurrences, track, time.Now())
	r.Map = BuildMap(context.Background(), r, tileStub{})
	var out bytes.Buffer
	err := WriteHTML(&out, r, photoStub{"a.jpg": {0xFF, 0xD8, 0xFF}}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{"São João do Tauape", "Fortaleza/CE", "<polyline points=", "data:image/png;base64,iVBORw0KGg", "© OpenStreetMap", `<span class="num">2</span>`, "data:image/jpeg;base64,/9j/", "&lt;script&gt;", "openstreetmap.org/?mlat=-3.731900", "1 h 15 min", "mantida em 1 de 2 registros", "3 pontos foram registrados"} {
		if !strings.Contains(html, want) {
			t.Errorf("report missing %q", want)
		}
	}
	if strings.Contains(html, "<script>") {
		t.Error("title rendered as markup")
	}
}

type tileStub struct{}

func (tileStub) Tile(context.Context, int, int, int) ([]byte, error) {
	return []byte("\x89PNG\r\n\x1a\ntile"), nil
}
func (tileStub) Attribution() string { return "© OpenStreetMap contributors" }

func TestProjectWebMercator(t *testing.T) {
	near := func(a, b float64) bool { return a-b < 0.01 && b-a < 0.01 }
	// At zoom 1 the world is 512×512 px: (0, 0) is the center, ±85.0511° the edges.
	cases := []struct{ lat, lng, x, y float64 }{{0, 0, 256, 256}, {85.0511, -180, 0, 0}, {-85.0511, 180, 512, 512}}
	for _, c := range cases {
		if x, y := project(c.lat, c.lng, 1); !near(x, c.x) || !near(y, c.y) {
			t.Errorf("project(%v, %v) = %v, %v", c.lat, c.lng, x, y)
		}
	}
}

func TestBuildMapFitsEveryPoint(t *testing.T) {
	w, occurrences := sample()
	track := []walk.TrackPoint{
		{Latitude: -3.7400, Longitude: -38.5400, RecordedAt: w.StartedAt},
		{Latitude: -3.7250, Longitude: -38.5150, RecordedAt: w.StartedAt.Add(time.Hour)},
	}
	view := BuildMap(context.Background(), Build(w, occurrences, track, time.Now()), nil)
	if view == nil || len(view.Markers) != 1 || view.Route == "" || view.Tiles != nil {
		t.Fatalf("got %+v", view)
	}
	for _, pair := range strings.Fields(view.Route) {
		var x, y float64
		fmt.Sscanf(pair, "%g,%g", &x, &y)
		if x < 0 || y < 0 || x > mapWidth || y > mapHeight {
			t.Errorf("route point %s outside the map", pair)
		}
	}
	if view := BuildMap(context.Background(), Build(w, nil, nil, time.Now()), nil); view != nil {
		t.Error("a walk without locations must not get a map")
	}
}

func TestOverlappingMarkersAreSpreadApart(t *testing.T) {
	// A chain of close points (one photo every few meters) must become one fan, not two overlapping ones.
	markers := []MapMarker{{X: 300, Y: 200, Number: 1}, {X: 302, Y: 201, Number: 2}, {X: 301, Y: 199, Number: 3}, {X: 500, Y: 100, Number: 4}}
	for i := range 6 {
		markers = append(markers, MapMarker{X: 200 + float64(i)*6, Y: 300 + float64(i)*6, Number: 5 + i})
	}
	spread(markers)
	for i := range markers {
		for j := i + 1; j < len(markers); j++ {
			if d := math.Hypot(markers[i].X-markers[j].X, markers[i].Y-markers[j].Y); d < 22 {
				t.Errorf("markers %d and %d still overlap (%.1f px apart)", markers[i].Number, markers[j].Number, d)
			}
		}
	}
	if !markers[0].Spread || markers[0].AnchorX != 300 || markers[3].Spread || !markers[9].Spread {
		t.Errorf("unexpected spread flags %+v", markers)
	}
}
