package main

import (
	"bairroacao/internal/ai"
	"bairroacao/internal/analyzer"
	"bairroacao/internal/api"
	"bairroacao/internal/photos"
	"bairroacao/internal/places"
	"bairroacao/internal/report"
	"bairroacao/internal/store"
	"bairroacao/internal/tiles"
	"bairroacao/internal/tlscert"
	"bairroacao/internal/walk"
	"bairroacao/internal/web"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if path := loadDotEnv(); path != "" {
		slog.Info("loaded environment file", "path", path)
	}
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	timeout, err := time.ParseDuration(env("OLLAMA_TIMEOUT", "300s"))
	if err != nil || timeout <= 0 || timeout > 15*time.Minute {
		return errors.New("OLLAMA_TIMEOUT must be between 0 and 15m")
	}
	model, err := ai.NewOllama(env("OLLAMA_URL", "http://localhost:11434"), env("OLLAMA_MODEL", "gemma3:4b"), timeout)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation(env("REPORT_TIMEZONE", "America/Sao_Paulo"))
	if err != nil {
		return fmt.Errorf("REPORT_TIMEZONE: %w", err)
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required; start PostgreSQL with `docker compose up -d` and copy .env.example to .env")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.OpenPostgres(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("DATABASE_URL: %w", err)
	}
	defer db.Close()
	photoDir, err := photos.Open(env("PHOTOS_DIR", "data/photos"))
	if err != nil {
		return fmt.Errorf("PHOTOS_DIR: %w", err)
	}

	// Map tiles for the downloadable report. MAP_TILES_URL=off draws only the route.
	var tileSource report.TileSource
	if tileURL := env("MAP_TILES_URL", "https://tile.openstreetmap.org/{z}/{x}/{y}.png"); tileURL != "off" {
		server, err := tiles.New(tileURL, env("TILE_CACHE_DIR", "data/tiles"), env("MAP_TILES_ATTRIBUTION", "© OpenStreetMap contributors"), "BairroEmAcao/0.1 (+https://github.com/Darrkkens)")
		if err != nil {
			return fmt.Errorf("MAP_TILES_URL: %w", err)
		}
		tileSource = server
	}

	// Neighborhood lists from OpenStreetMap; public Overpass servers are tried in order.
	overpass, err := places.NewOverpass(strings.Split(env("OVERPASS_URLS", "https://overpass-api.de/api/interpreter,https://maps.mail.ru/osm/tools/overpass/api/interpreter,https://overpass.private.coffee/api/interpreter"), ","), "BairroEmAcao/0.1 (+https://github.com/Darrkkens)", db)
	if err != nil {
		return fmt.Errorf("OVERPASS_URLS: %w", err)
	}

	worker := analyzer.New(db, model, photoDir)
	walks := &walk.Service{Repo: db, Photos: photoDir, Wake: worker.Wake}
	var handler http.Handler = api.New(api.Config{
		Walks: walks, Neighborhoods: overpass, Photos: photoDir, Tiles: tileSource, AI: model, Database: db, Location: location,
		Origins: strings.Split(env("CORS_ORIGINS", "http://localhost:5174,http://127.0.0.1:5174"), ","),
	})
	addr := env("API_ADDR", "127.0.0.1:8090")
	// Notebook server mode: serve the built app too (WEB_DIR) over trusted HTTPS (TLS=auto).
	var certFile, keyFile, caFile string
	if os.Getenv("TLS") == "auto" {
		files, err := tlscert.Ensure(env("TLS_DIR", "data/tls"))
		if err != nil {
			return fmt.Errorf("TLS: %w", err)
		}
		certFile, keyFile, caFile = files.Cert, files.Key, files.CA
	} else if os.Getenv("TLS_CERT") != "" {
		certFile, keyFile = os.Getenv("TLS_CERT"), os.Getenv("TLS_KEY")
	}
	if dir := os.Getenv("WEB_DIR"); dir != "" {
		if handler, err = web.Handler(dir, handler, caFile); err != nil {
			return fmt.Errorf("WEB_DIR: %w", err)
		}
	}
	if host, _, _ := net.SplitHostPort(addr); certFile == "" && host != "127.0.0.1" && host != "localhost" {
		slog.Warn("serving plain HTTP beyond this computer; phones block location and offline use without HTTPS (set TLS=auto)")
	}
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}

	// Photos stored before grouping existed get their signature in the background.
	go walks.BackfillSignatures(ctx)

	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		worker.Run(ctx)
	}()
	if !model.Healthy(ctx) {
		slog.Warn("Ollama or model not available yet; occurrences will wait in the queue", "model", model.Model())
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("Bairro em Ação API ready", "address", server.Addr, "model", model.Model())
		if certFile == "" {
			errCh <- server.ListenAndServe()
			return
		}
		for _, url := range phoneURLs(addr) {
			slog.Info("open on the phone (same Wi-Fi)", "url", url)
		}
		errCh <- server.ListenAndServeTLS(certFile, keyFile)
	}()
	select {
	case err := <-errCh:
		stop()
		<-workerDone
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := server.Shutdown(shutdown)
		<-workerDone
		if err != nil {
			_ = server.Close()
			return err
		}
	}
	return nil
}

// phoneURLs lists the addresses a phone on the same network can open.
func phoneURLs(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return []string{"https://" + net.JoinHostPort(host, port)}
	}
	var urls []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		// Docker bridges (172.16/12) are private too but unreachable from a phone.
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.To4() == nil || !n.IP.IsPrivate() {
			continue
		}
		if ip := n.IP.To4(); ip[0] == 172 && ip[1]&0xf0 == 16 {
			continue
		}
		urls = append(urls, "https://"+net.JoinHostPort(n.IP.String(), port))
	}
	return urls
}
