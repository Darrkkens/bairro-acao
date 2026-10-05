package places

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type memoryCache struct {
	names map[string][]string
	at    map[string]time.Time
}

func newCache() *memoryCache {
	return &memoryCache{names: map[string][]string{}, at: map[string]time.Time{}}
}

func (m *memoryCache) NeighborhoodList(_ context.Context, code string) ([]string, time.Time, bool, error) {
	names, ok := m.names[code]
	return names, m.at[code], ok, nil
}

func (m *memoryCache) SaveNeighborhoodList(_ context.Context, code string, names []string, at time.Time) error {
	m.names[code], m.at[code] = names, at
	return nil
}

const joacaba = `{"elements":[{"tags":{"name":"Vila Remor"}},{"tags":{"name":"Centro"}},{"tags":{"name":"Água Verde"}},{"tags":{"name":"Joaçaba"}},{"tags":{"name":"centro"}},{"tags":{}}]}`

func TestFallsBackToTheNextEndpointAndCaches(t *testing.T) {
	var hits atomic.Int32
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "busy", http.StatusGatewayTimeout) }))
	defer down.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.UserAgent() != "BairroEmAcao/test" || !strings.Contains(r.FormValue("data"), `"IBGE:GEOCODIGO"="4209003"`) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(joacaba))
	}))
	defer up.Close()
	cache := newCache()
	o, err := NewOverpass([]string{down.URL, up.URL}, "BairroEmAcao/test", cache)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		list, err := o.Neighborhoods(context.Background(), "4209003", "Joaçaba")
		if err != nil {
			t.Fatal(err)
		}
		// Sorted in Portuguese order, duplicates and the city's own name removed.
		if strings.Join(list.Names, ",") != "Água Verde,Centro,Vila Remor" {
			t.Fatalf("got %v", list.Names)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("Overpass queried %d times; the second call must come from the cache", hits.Load())
	}
}

func TestServesAStaleListWhenOverpassIsDown(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "busy", http.StatusTooManyRequests) }))
	defer down.Close()
	cache := newCache()
	cache.names["4209003"], cache.at["4209003"] = []string{"Centro"}, time.Now().Add(-2*RefreshAfter)
	o, _ := NewOverpass([]string{down.URL}, "ua", cache)
	if list, err := o.Neighborhoods(context.Background(), "4209003", ""); err != nil || len(list.Names) != 1 {
		t.Fatalf("got %v, %v", list, err)
	}
	if _, err := o.Neighborhoods(context.Background(), "4205407", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("uncached city with Overpass down: got %v", err)
	}
	if _, err := o.Neighborhoods(context.Background(), "42; drop", ""); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("bad code: got %v", err)
	}
}

func TestOverpassRuntimeErrorIsAFailure(t *testing.T) {
	timeout := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"remark":"runtime error: Query timed out in \"query\" at line 3 after 26 seconds.","elements":[]}`))
	}))
	defer timeout.Close()
	o, _ := NewOverpass([]string{timeout.URL}, "ua", newCache())
	if _, err := o.Neighborhoods(context.Background(), "4209003", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a timed-out query must not be cached as an empty list: got %v", err)
	}
}
