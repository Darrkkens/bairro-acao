package tiles

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestTileIsFetchedOnceThenCached(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nrest")
	var hits atomic.Int32
	var agent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		agent = r.UserAgent()
		if r.URL.Path != "/15/12345/23456.png" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(png)
	}))
	defer server.Close()
	s, err := New(server.URL+"/{z}/{x}/{y}.png", t.TempDir(), "© OpenStreetMap", "BairroEmAcao/test")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		data, err := s.Tile(context.Background(), 15, 12345, 23456)
		if err != nil || string(data) != string(png) {
			t.Fatalf("got %q %v", data, err)
		}
	}
	if hits.Load() != 1 || agent != "BairroEmAcao/test" {
		t.Fatalf("hits %d, user agent %q", hits.Load(), agent)
	}
	if _, err := s.Tile(context.Background(), 15, 1, 1); err == nil {
		t.Error("404 must be an error")
	}
	if _, err := s.Tile(context.Background(), 2, 4, 0); err == nil {
		t.Error("x outside the zoom level accepted")
	}
}

func TestNewRejectsBadTemplates(t *testing.T) {
	for _, bad := range []string{"", "file:///{z}/{x}/{y}", "https://tiles.example/{z}/{x}.png"} {
		if _, err := New(bad, t.TempDir(), "", "ua"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
