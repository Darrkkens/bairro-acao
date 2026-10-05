package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServesAppAPIAndCertificate(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>app"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "assets", "index-abc.js"), []byte("console.log(1)"), 0o644)
	ca := filepath.Join(t.TempDir(), "ca.crt")
	_ = os.WriteFile(ca, []byte{0x30, 0x82}, 0o644)
	secret := filepath.Join(filepath.Dir(dir), "secret.txt")
	_ = os.WriteFile(secret, []byte("nope"), 0o644)
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("api")) })
	h, err := Handler(dir, api, ca)
	if err != nil {
		t.Fatal(err)
	}
	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}
	if rec := get("/api/health"); rec.Body.String() != "api" {
		t.Errorf("api not routed: %q", rec.Body)
	}
	if rec := get("/"); !strings.Contains(rec.Body.String(), "app") || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("index: %q %q", rec.Body, rec.Header().Get("Cache-Control"))
	}
	if rec := get("/assets/index-abc.js"); !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("assets cache: %q", rec.Header().Get("Cache-Control"))
	}
	if rec := get("/caminhadas/qualquer"); !strings.Contains(rec.Body.String(), "app") {
		t.Errorf("unknown paths must get the app")
	}
	if rec := get("/../secret.txt"); strings.Contains(rec.Body.String(), "nope") {
		t.Error("path traversal served a file outside WEB_DIR")
	}
	if rec := get("/ca.crt"); rec.Header().Get("Content-Type") != "application/x-x509-ca-cert" {
		t.Errorf("ca.crt type %q", rec.Header().Get("Content-Type"))
	}
	if _, err := Handler(t.TempDir(), api, ""); err == nil {
		t.Error("an empty WEB_DIR must be rejected")
	}
}
