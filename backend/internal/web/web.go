// Package web serves the built app next to the API, so a single process on
// the notebook is everything the phone needs.
package web

import (
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Handler sends /api/ to api and everything else to the files in dir (a Vite
// build). caFile, when set, is offered at /ca.crt for installing on the phone.
func Handler(dir string, api http.Handler, caFile string) (http.Handler, error) {
	index := filepath.Join(dir, "index.html")
	if _, err := os.Stat(index); err != nil {
		return nil, errors.New("no index.html in WEB_DIR; run `npm run build` in frontend/ first")
	}
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if caFile != "" && r.URL.Path == "/ca.crt" {
			h.Set("Content-Type", "application/x-x509-ca-cert")
			h.Set("Content-Disposition", `attachment; filename="bairro-em-acao-ca.crt"`)
			http.ServeFile(w, r, caFile)
			return
		}
		// Build assets carry a content hash; everything else (index, service worker) must revalidate.
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		clean := path.Clean("/" + r.URL.Path)
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(clean))); err != nil || info.IsDir() {
			// The app routes with the URL hash, so any other path is the app itself.
			http.ServeFile(w, r, index)
			return
		}
		files.ServeHTTP(w, r)
	}), nil
}
