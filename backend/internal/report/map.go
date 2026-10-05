package report

import (
	"bairroacao/internal/walk"
	"context"
	"encoding/base64"
	"fmt"
	"html/template"
	"math"
	"slices"
	"strings"
	"sync"
)

const (
	tileSize  = 256
	mapWidth  = 640
	mapHeight = 400
	mapPad    = 36
)

// TileSource provides map tiles; nil draws the route on a plain background.
type TileSource interface {
	Tile(ctx context.Context, z, x, y int) ([]byte, error)
	Attribution() string
}

type MapTile struct {
	X, Y int
	Src  template.URL
}

type MapMarker struct {
	X, Y   float64
	Number int
	Color  string
	// Spread markers were moved apart from others at nearly the same spot;
	// a leader line goes back to the real location at AnchorX, AnchorY.
	Spread           bool
	AnchorX, AnchorY float64
}

// MapView is a static map: tiles embedded as data URIs plus an SVG overlay,
// so the report keeps working offline and without scripts.
type MapView struct {
	Width, Height int
	Tiles         []MapTile
	Route         string
	Start         *MapMarker
	Markers       []MapMarker
	Attribution   string
	// OpenURL opens the same area on openstreetmap.org, where it can be zoomed.
	OpenURL string
}

// markerSpacing is the minimum distance between marker centers, in map pixels.
const markerSpacing = 32

// fanRadius is the circle a group of k markers is spread on.
func fanRadius(k int) float64 {
	if k < 2 {
		return 0
	}
	return math.Max(26, float64(k)*markerSpacing/(2*math.Pi))
}

// spread fans out markers that would cover each other (several photos of the
// same corner), keeping every number visible. Groups whose fans would touch
// are merged until none overlap.
func spread(markers []MapMarker) {
	type group struct {
		members []int
		x, y    float64
	}
	groups := make([]group, len(markers))
	for i, m := range markers {
		groups[i] = group{[]int{i}, m.X, m.Y}
	}
	for merged := true; merged; {
		merged = false
		for a := 0; a < len(groups) && !merged; a++ {
			for b := a + 1; b < len(groups); b++ {
				ga, gb := groups[a], groups[b]
				if math.Hypot(ga.x-gb.x, ga.y-gb.y) >= fanRadius(len(ga.members))+fanRadius(len(gb.members))+markerSpacing {
					continue
				}
				members := append(append([]int{}, ga.members...), gb.members...)
				var x, y float64
				for _, i := range members {
					x += markers[i].X / float64(len(members))
					y += markers[i].Y / float64(len(members))
				}
				groups[a] = group{members, x, y}
				groups = append(groups[:b], groups[b+1:]...)
				merged = true
				break
			}
		}
	}
	for _, g := range groups {
		k := len(g.members)
		if k < 2 {
			continue
		}
		// Numbers run clockwise from the top, as in the app.
		slices.SortFunc(g.members, func(a, b int) int { return markers[a].Number - markers[b].Number })
		for n, i := range g.members {
			angle := -math.Pi/2 + 2*math.Pi*float64(n)/float64(k)
			m := &markers[i]
			m.Spread, m.AnchorX, m.AnchorY = true, m.X, m.Y
			m.X = math.Round(math.Max(16, math.Min(mapWidth-16, g.x+fanRadius(k)*math.Cos(angle)))*10) / 10
			m.Y = math.Round(math.Max(16, math.Min(mapHeight-16, g.y+fanRadius(k)*math.Sin(angle)))*10) / 10
		}
	}
}

var markerColors = map[walk.Category]string{
	walk.Cleaning: "#a15c07", walk.Sidewalks: "#6d4bc4", walk.Street: "#c2410c", walk.Leisure: "#15803d", walk.Other: "#5b6470",
}

// project converts WGS84 coordinates to Web Mercator pixels at zoom z.
func project(lat, lng float64, z int) (float64, float64) {
	n := math.Exp2(float64(z)) * tileSize
	lat = math.Max(-85.0511, math.Min(85.0511, lat))
	rad := lat * math.Pi / 180
	return (lng + 180) / 360 * n, (1 - math.Log(math.Tan(rad)+1/math.Cos(rad))/math.Pi) / 2 * n
}

type latLng struct{ lat, lng float64 }

// zoomFor picks the closest zoom at which every point fits inside the padded frame.
func zoomFor(points []latLng) int {
	for z := 18; z > 2; z-- {
		minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, p := range points {
			x, y := project(p.lat, p.lng, z)
			minX, minY, maxX, maxY = math.Min(minX, x), math.Min(minY, y), math.Max(maxX, x), math.Max(maxY, y)
		}
		if maxX-minX <= mapWidth-2*mapPad && maxY-minY <= mapHeight-2*mapPad {
			return min(z, 17) // a single point at 18 shows little context
		}
	}
	return 2
}

// BuildMap returns nil when the walk has no location at all.
func BuildMap(ctx context.Context, r Report, source TileSource) *MapView {
	var points []latLng
	for _, p := range r.Route {
		points = append(points, latLng{p.Latitude, p.Longitude})
	}
	for _, s := range r.Sections {
		for _, item := range s.Items {
			if item.Location != nil {
				points = append(points, latLng{item.Location.Latitude, item.Location.Longitude})
			}
		}
	}
	if len(points) == 0 {
		return nil
	}
	z := zoomFor(points)
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range points {
		x, y := project(p.lat, p.lng, z)
		minX, minY, maxX, maxY = math.Min(minX, x), math.Min(minY, y), math.Max(maxX, x), math.Max(maxY, y)
	}
	originX := math.Round((minX+maxX)/2 - mapWidth/2)
	originY := math.Round((minY+maxY)/2 - mapHeight/2)
	view := &MapView{Width: mapWidth, Height: mapHeight}
	at := func(lat, lng float64) (float64, float64) {
		x, y := project(lat, lng, z)
		return math.Round((x-originX)*10) / 10, math.Round((y-originY)*10) / 10
	}

	var route []string
	for _, p := range r.Route {
		x, y := at(p.Latitude, p.Longitude)
		route = append(route, fmt.Sprintf("%g,%g", x, y))
	}
	if len(route) > 1 {
		view.Route = strings.Join(route, " ")
		x, y := at(r.Route[0].Latitude, r.Route[0].Longitude)
		view.Start = &MapMarker{X: x, Y: y}
	}
	for _, s := range r.Sections {
		for _, item := range s.Items {
			if item.Location != nil {
				x, y := at(item.Location.Latitude, item.Location.Longitude)
				view.Markers = append(view.Markers, MapMarker{X: x, Y: y, Number: item.Number, Color: markerColors[item.Category]})
			}
		}
	}

	spread(view.Markers)
	// Center of the frame back in degrees, for the interactive link.
	n := math.Exp2(float64(z)) * tileSize
	centerX, centerY := originX+mapWidth/2, originY+mapHeight/2
	lng := centerX/n*360 - 180
	lat := math.Atan(math.Sinh(math.Pi*(1-2*centerY/n))) * 180 / math.Pi
	view.OpenURL = fmt.Sprintf("https://www.openstreetmap.org/#map=%d/%.5f/%.5f", z, lat, lng)

	if source != nil {
		view.Attribution = source.Attribution()
		view.Tiles = fetchTiles(ctx, source, z, originX, originY)
	}
	return view
}

func fetchTiles(ctx context.Context, source TileSource, z int, originX, originY float64) []MapTile {
	n := 1 << z
	var tiles []MapTile
	var mu sync.Mutex
	var wg sync.WaitGroup
	for ty := int(math.Floor(originY / tileSize)); ty <= int(math.Floor((originY+mapHeight-1)/tileSize)); ty++ {
		for tx := int(math.Floor(originX / tileSize)); tx <= int(math.Floor((originX+mapWidth-1)/tileSize)); tx++ {
			if ty < 0 || ty >= n {
				continue
			}
			wg.Add(1)
			go func(tx, ty int) {
				defer wg.Done()
				data, err := source.Tile(ctx, z, ((tx%n)+n)%n, ty)
				if err != nil {
					return // the overlay still renders on the plain background
				}
				tile := MapTile{X: tx*tileSize - int(originX), Y: ty*tileSize - int(originY), Src: template.URL("data:" + imageType(data) + ";base64," + base64.StdEncoding.EncodeToString(data))}
				mu.Lock()
				tiles = append(tiles, tile)
				mu.Unlock()
			}(tx, ty)
		}
	}
	wg.Wait()
	return tiles
}

func imageType(data []byte) string {
	switch {
	case len(data) > 2 && data[0] == 0xFF && data[1] == 0xD8:
		return "image/jpeg"
	case len(data) > 4 && string(data[:4]) == "RIFF":
		return "image/webp"
	}
	return "image/png"
}
