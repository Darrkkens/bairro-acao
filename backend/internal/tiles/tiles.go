// Package tiles fetches map tiles for the downloadable report and caches them
// on disk, so rebuilding a report does not hit the tile server again.
package tiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cacheFor follows the OpenStreetMap tile policy, which asks clients to keep tiles for at least 7 days.
const cacheFor = 30 * 24 * time.Hour

var ErrUnavailable = errors.New("tile unavailable")

type Server struct {
	template, dir, userAgent, attribution string
	http                                  *http.Client
	// OpenStreetMap allows at most two parallel downloads per client.
	slots chan struct{}
}

// New takes a URL template such as https://tile.openstreetmap.org/{z}/{x}/{y}.png.
func New(template, dir, attribution, userAgent string) (*Server, error) {
	u, err := url.Parse(strings.NewReplacer("{z}", "0", "{x}", "0", "{y}", "0").Replace(template))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || !strings.Contains(template, "{z}") || !strings.Contains(template, "{x}") || !strings.Contains(template, "{y}") {
		return nil, errors.New("map tile URL must be http(s) and contain {z}, {x} and {y}")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Server{template: template, dir: dir, userAgent: userAgent, attribution: attribution, http: &http.Client{Timeout: 10 * time.Second}, slots: make(chan struct{}, 2)}, nil
}

func (s *Server) Attribution() string { return s.attribution }

func (s *Server) Tile(ctx context.Context, z, x, y int) ([]byte, error) {
	if z < 0 || z > 19 || x < 0 || y < 0 || x >= 1<<z || y >= 1<<z {
		return nil, ErrUnavailable
	}
	path := filepath.Join(s.dir, strconv.Itoa(z), strconv.Itoa(x), strconv.Itoa(y))
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < cacheFor {
		if data, err := os.ReadFile(path); err == nil {
			return data, nil
		}
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	tileURL := strings.NewReplacer("{z}", strconv.Itoa(z), "{x}", strconv.Itoa(x), "{y}", strconv.Itoa(y)).Replace(s.template)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tileURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.userAgent)
	res, err := s.http.Do(req)
	if err != nil {
		return s.stale(path, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil || res.StatusCode != http.StatusOK || !isImage(data) {
		return s.stale(path, fmt.Errorf("%w: %s returned %d", ErrUnavailable, tileURL, res.StatusCode))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err == nil {
		tmp := path + ".tmp"
		if os.WriteFile(tmp, data, 0o640) == nil {
			_ = os.Rename(tmp, path)
		}
	}
	return data, nil
}

// stale serves an expired cached tile when the server cannot be reached.
func (s *Server) stale(path string, cause error) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		return data, nil
	}
	return nil, cause
}

func isImage(data []byte) bool {
	return len(data) > 8 && (string(data[:8]) == "\x89PNG\r\n\x1a\n" || (data[0] == 0xFF && data[1] == 0xD8) || string(data[:4]) == "RIFF")
}
