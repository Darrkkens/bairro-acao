// Package places lists a city's neighborhoods from OpenStreetMap through the
// Overpass API. Public Overpass servers are often overloaded, so several are
// tried in turn and every list that arrives is kept in PostgreSQL: once a city
// has been fetched, its list survives Overpass being down.
package places

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

var ErrUnavailable = errors.New("A lista de bairros não pôde ser carregada agora. Digite o nome do bairro ou tente de novo em instantes.")
var ErrInvalidCode = errors.New("Código IBGE de município inválido.")

// RefreshAfter is how long a stored list is used before asking Overpass again.
const RefreshAfter = 30 * 24 * time.Hour

type Cache interface {
	NeighborhoodList(ctx context.Context, code string) ([]string, time.Time, bool, error)
	SaveNeighborhoodList(ctx context.Context, code string, names []string, at time.Time) error
}

type List struct {
	Names     []string  `json:"names"`
	FetchedAt time.Time `json:"fetched_at"`
	Source    string    `json:"source"`
}

type Overpass struct {
	endpoints []string
	userAgent string
	cache     Cache
	http      *http.Client
	mu        sync.Mutex
	inflight  map[string]*call
}

type call struct {
	done chan struct{}
	list List
	err  error
}

func NewOverpass(endpoints []string, userAgent string, cache Cache) (*Overpass, error) {
	var clean []string
	for _, e := range endpoints {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		u, err := url.Parse(e)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("invalid Overpass endpoint %q", e)
		}
		clean = append(clean, e)
	}
	if len(clean) == 0 {
		return nil, errors.New("at least one Overpass endpoint is required")
	}
	return &Overpass{endpoints: clean, userAgent: userAgent, cache: cache, http: &http.Client{Timeout: 35 * time.Second}, inflight: map[string]*call{}}, nil
}

var codePattern = regexp.MustCompile(`^\d{7}$`)

// Neighborhoods returns the city's neighborhoods sorted in Portuguese order.
// city (optional) drops an entry named like the municipality itself.
func (o *Overpass) Neighborhoods(ctx context.Context, code, city string) (List, error) {
	if !codePattern.MatchString(code) {
		return List{}, ErrInvalidCode
	}
	names, at, cached, err := o.cache.NeighborhoodList(ctx, code)
	if err != nil {
		slog.Warn("neighborhood cache unavailable", "error", err)
	}
	if cached && time.Since(at) < RefreshAfter {
		return List{Names: withoutCity(names, city), FetchedAt: at, Source: "openstreetmap"}, nil
	}
	list, err := o.fetchOnce(ctx, code)
	if err != nil {
		if cached { // stale beats nothing
			return List{Names: withoutCity(names, city), FetchedAt: at, Source: "openstreetmap"}, nil
		}
		return List{}, err
	}
	list.Names = withoutCity(list.Names, city)
	return list, nil
}

// fetchOnce makes concurrent requests for the same city share one Overpass query.
func (o *Overpass) fetchOnce(ctx context.Context, code string) (List, error) {
	o.mu.Lock()
	if c, ok := o.inflight[code]; ok {
		o.mu.Unlock()
		select {
		case <-c.done:
			return c.list, c.err
		case <-ctx.Done():
			return List{}, ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	o.inflight[code] = c
	o.mu.Unlock()
	// The query outlives a client that gives up, so the next visit finds it stored.
	c.list, c.err = o.fetch(context.WithoutCancel(ctx), code)
	close(c.done)
	o.mu.Lock()
	delete(o.inflight, code)
	o.mu.Unlock()
	return c.list, c.err
}

func (o *Overpass) fetch(ctx context.Context, code string) (List, error) {
	// Neighborhoods are mapped as place nodes/areas or as administrative boundaries (levels 9–10).
	query := fmt.Sprintf(`[out:json][timeout:25];
area["IBGE:GEOCODIGO"="%s"]["boundary"="administrative"]->.city;
(
  nwr(area.city)["place"~"^(suburb|neighbourhood|quarter)$"];
  relation(area.city)["boundary"="administrative"]["admin_level"~"^(9|10)$"];
);
out tags;`, code)
	for _, endpoint := range o.endpoints {
		names, err := o.query(ctx, endpoint, query)
		if err != nil {
			slog.Warn("Overpass endpoint failed", "endpoint", endpoint, "error", err)
			continue
		}
		now := time.Now()
		if err := o.cache.SaveNeighborhoodList(ctx, code, names, now); err != nil {
			slog.Warn("saving neighborhood list failed", "code", code, "error", err)
		}
		return List{Names: names, FetchedAt: now, Source: "openstreetmap"}, nil
	}
	return List{}, ErrUnavailable
}

func (o *Overpass) query(ctx context.Context, endpoint, query string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(url.Values{"data": {query}}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", o.userAgent)
	res, err := o.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", res.StatusCode)
	}
	var body struct {
		Remark   string `json:"remark"`
		Elements []struct {
			Tags map[string]string `json:"tags"`
		} `json:"elements"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	// Overpass answers 200 with a remark when the query hit its time or memory limit.
	if strings.Contains(body.Remark, "error") {
		return nil, fmt.Errorf("query failed: %s", body.Remark)
	}
	seen := map[string]bool{}
	var names []string
	for _, e := range body.Elements {
		name := strings.Join(strings.Fields(e.Tags["name"]), " ")
		if name != "" && !seen[fold(name)] {
			seen[fold(name)] = true
			names = append(names, name)
		}
	}
	collate.New(language.BrazilianPortuguese).SortStrings(names)
	return names, nil
}

// fold compares names ignoring case and accents.
func fold(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func withoutCity(names []string, city string) []string {
	if strings.TrimSpace(city) == "" {
		return names
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if fold(n) != fold(strings.TrimSpace(city)) {
			out = append(out, n)
		}
	}
	return out
}
