package api

import (
	"bairroacao/internal/photos"
	"bairroacao/internal/places"
	"bairroacao/internal/report"
	"bairroacao/internal/walk"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MaxPhotoBytes bounds one upload; the app resizes photos to ~1600px before sending.
const MaxPhotoBytes = 8 << 20

type Health interface {
	Model() string
	Healthy(context.Context) bool
}

// Neighborhoods lists a municipality's neighborhoods (see internal/places).
type Neighborhoods interface {
	Neighborhoods(ctx context.Context, code, city string) (places.List, error)
}

type Server struct {
	walks         *walk.Service
	neighborhoods Neighborhoods
	photos        *photos.Dir
	tiles         report.TileSource
	ai            Health
	database      interface{ Ping(context.Context) error }
	location      *time.Location
	origins       map[string]bool
}

type Config struct {
	Walks         *walk.Service
	Neighborhoods Neighborhoods
	Photos        *photos.Dir
	// Tiles draws the map in the downloadable report; nil keeps only the route.
	Tiles    report.TileSource
	AI       Health
	Database interface{ Ping(context.Context) error }
	// Location formats dates in the downloadable report.
	Location *time.Location
	Origins  []string
}

func New(c Config) http.Handler {
	s := &Server{walks: c.Walks, neighborhoods: c.Neighborhoods, photos: c.Photos, tiles: c.Tiles, ai: c.AI, database: c.Database, location: c.Location, origins: map[string]bool{}}
	if s.location == nil {
		s.location = time.Local
	}
	for _, o := range c.Origins {
		if o = strings.TrimSpace(o); o != "" {
			s.origins[o] = true
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/categories", s.categories)
	mux.HandleFunc("GET /api/walks", s.listWalks)
	mux.HandleFunc("PUT /api/walks/{id}", s.startWalk)
	mux.HandleFunc("GET /api/walks/{id}", s.getWalk)
	mux.HandleFunc("POST /api/walks/{id}/finish", s.finishWalk)
	mux.HandleFunc("GET /api/walks/{id}/track", s.track)
	mux.HandleFunc("GET /api/walks/{id}/report.html", s.reportHTML)
	mux.HandleFunc("PUT /api/walks/{id}/occurrences/{occurrence}", s.record)
	mux.HandleFunc("PATCH /api/occurrences/{id}", s.review)
	mux.HandleFunc("POST /api/occurrences/{id}/analyze", s.reanalyze)
	mux.HandleFunc("DELETE /api/occurrences/{id}", s.deleteOccurrence)
	mux.HandleFunc("POST /api/occurrences/{id}/group", s.group)
	mux.HandleFunc("POST /api/occurrences/{id}/ungroup", s.ungroup)
	mux.HandleFunc("POST /api/occurrences/{id}/keep-separate", s.keepSeparate)
	mux.HandleFunc("GET /api/photos/{name}", s.photo)
	mux.HandleFunc("GET /api/places/neighborhoods/{code}", s.listNeighborhoods)
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if origin := r.Header.Get("Origin"); origin != "" && s.origins[origin] {
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, PUT, POST, PATCH, DELETE")
				h.Set("Access-Control-Allow-Headers", "Content-Type")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return readJSONLimit(w, r, v, 16<<10)
}

func readJSONLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "Use Content-Type: application/json.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	err = d.Decode(v)
	if err == nil {
		var extra any
		if d.Decode(&extra) != io.EOF {
			err = errors.New("multiple JSON values")
		}
	}
	if err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			fail(w, http.StatusRequestEntityTooLarge, "O corpo da requisição é grande demais.")
		} else {
			fail(w, http.StatusBadRequest, "Requisição JSON inválida ou com campos inesperados.")
		}
		return false
	}
	return true
}

func serviceError(w http.ResponseWriter, err error) {
	var invalid *walk.ValidationError
	switch {
	case errors.As(err, &invalid):
		fail(w, http.StatusBadRequest, invalid.Message)
	case errors.Is(err, walk.ErrNotFound), errors.Is(err, photos.ErrNotFound):
		fail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, places.ErrInvalidCode):
		fail(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, places.ErrUnavailable):
		w.Header().Set("Retry-After", "30")
		fail(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		fail(w, http.StatusGatewayTimeout, "A requisição demorou demais. Tente novamente.")
	case errors.Is(err, context.Canceled):
		fail(w, http.StatusRequestTimeout, "Requisição cancelada.")
	default:
		slog.Error("request failed", "error", err)
		fail(w, http.StatusInternalServerError, "Não foi possível concluir a requisição.")
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	database := s.database.Ping(r.Context()) == nil
	status := http.StatusOK
	if !database {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"database": database, "ai": map[string]any{"model": s.ai.Model(), "available": s.ai.Healthy(r.Context())}})
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, walk.Categories)
}

func (s *Server) listWalks(w http.ResponseWriter, r *http.Request) {
	walks, err := s.walks.Walks(r.Context())
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, walks)
}

func (s *Server) startWalk(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Neighborhood string    `json:"neighborhood"`
		City         string    `json:"city"`
		State        string    `json:"state"`
		StartedAt    time.Time `json:"started_at"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	result, created, err := s.walks.StartWalk(r.Context(), walk.Walk{ID: r.PathValue("id"), Neighborhood: req.Neighborhood, City: req.City, State: req.State, StartedAt: req.StartedAt})
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], result)
}

type walkResponse struct {
	walk.Walk
	Occurrences []walk.Occurrence `json:"occurrences"`
}

func (s *Server) getWalk(w http.ResponseWriter, r *http.Request) {
	result, occurrences, err := s.walks.Walk(r.Context(), r.PathValue("id"))
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, walkResponse{result, occurrences})
}

func (s *Server) finishWalk(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FinishedAt time.Time         `json:"finished_at"`
		Track      []walk.TrackPoint `json:"track"`
	}
	// A long walk sends a few thousand GPS points.
	if !readJSONLimit(w, r, &req, 1<<20) {
		return
	}
	result, err := s.walks.FinishWalk(r.Context(), r.PathValue("id"), req.FinishedAt, req.Track)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) track(w http.ResponseWriter, r *http.Request) {
	points, err := s.walks.Track(r.Context(), r.PathValue("id"))
	if err != nil {
		serviceError(w, err)
		return
	}
	if points == nil {
		points = []walk.TrackPoint{}
	}
	writeJSON(w, http.StatusOK, points)
}

func (s *Server) reportHTML(w http.ResponseWriter, r *http.Request) {
	result, occurrences, err := s.walks.Walk(r.Context(), r.PathValue("id"))
	if err != nil {
		serviceError(w, err)
		return
	}
	track, err := s.walks.Track(r.Context(), result.ID)
	if err != nil {
		serviceError(w, err)
		return
	}
	rep := report.Build(result, occurrences, track, time.Now())
	rep.Map = report.BuildMap(r.Context(), rep, s.tiles)
	var page bytes.Buffer
	if err := report.WriteHTML(&page, rep, s.photos, s.location); err != nil {
		serviceError(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	// The page is a document, not part of the app: no scripts, no remote loads.
	h.Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'")
	disposition := "inline"
	if r.URL.Query().Has("download") {
		disposition = "attachment"
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": report.Filename(rep, s.location)}))
	_, _ = page.WriteTo(w)
}

// record receives one occurrence as multipart/form-data: photo, note,
// captured_at (RFC 3339) and optionally latitude, longitude and accuracy_m.
func (s *Server) record(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxPhotoBytes+64<<10)
	if err := r.ParseMultipartForm(MaxPhotoBytes + 64<<10); err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			fail(w, http.StatusRequestEntityTooLarge, "A foto é grande demais (máximo de 8 MB).")
		} else {
			fail(w, http.StatusBadRequest, "Envie o registro como multipart/form-data.")
		}
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, _, err := r.FormFile("photo")
	if err != nil {
		fail(w, http.StatusBadRequest, "A foto do registro é obrigatória.")
		return
	}
	defer file.Close()
	photo, err := io.ReadAll(io.LimitReader(file, MaxPhotoBytes+1))
	if err != nil || len(photo) > MaxPhotoBytes {
		fail(w, http.StatusRequestEntityTooLarge, "A foto é grande demais (máximo de 8 MB).")
		return
	}
	capturedAt, err := time.Parse(time.RFC3339, r.FormValue("captured_at"))
	if err != nil {
		fail(w, http.StatusBadRequest, "Informe captured_at no formato RFC 3339.")
		return
	}
	location, err := formLocation(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "Localização inválida.")
		return
	}
	result, created, err := s.walks.Record(r.Context(), walk.NewOccurrence{
		ID: r.PathValue("occurrence"), WalkID: r.PathValue("id"), Photo: photo,
		Note: r.FormValue("note"), Location: location, CapturedAt: capturedAt,
	})
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, map[bool]int{true: http.StatusCreated, false: http.StatusOK}[created], result)
}

func formLocation(r *http.Request) (*walk.Location, error) {
	lat, lng := r.FormValue("latitude"), r.FormValue("longitude")
	if lat == "" && lng == "" {
		return nil, nil
	}
	var l walk.Location
	var err error
	if l.Latitude, err = strconv.ParseFloat(lat, 64); err != nil {
		return nil, err
	}
	if l.Longitude, err = strconv.ParseFloat(lng, 64); err != nil {
		return nil, err
	}
	if raw := r.FormValue("accuracy_m"); raw != "" {
		accuracy, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, err
		}
		l.Accuracy = &accuracy
	}
	return &l, nil
}

func (s *Server) review(w http.ResponseWriter, r *http.Request) {
	var req walk.Review
	if !readJSON(w, r, &req) {
		return
	}
	result, err := s.walks.Review(r.Context(), r.PathValue("id"), req)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) reanalyze(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note string `json:"note"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	result, err := s.walks.Reanalyze(r.Context(), r.PathValue("id"), req.Note)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) deleteOccurrence(w http.ResponseWriter, r *http.Request) {
	if err := s.walks.Delete(r.Context(), r.PathValue("id")); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listNeighborhoods(w http.ResponseWriter, r *http.Request) {
	if s.neighborhoods == nil {
		serviceError(w, places.ErrUnavailable)
		return
	}
	list, err := s.neighborhoods.Neighborhoods(r.Context(), r.PathValue("code"), r.URL.Query().Get("city"))
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// group makes the occurrence an extra photo of the point named in "with".
func (s *Server) group(w http.ResponseWriter, r *http.Request) {
	var req struct {
		With string `json:"with"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.walks.Group(r.Context(), r.PathValue("id"), req.With); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ungroup(w http.ResponseWriter, r *http.Request) {
	if err := s.walks.Ungroup(r.Context(), r.PathValue("id")); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) keepSeparate(w http.ResponseWriter, r *http.Request) {
	if err := s.walks.KeepSeparate(r.Context(), r.PathValue("id")); err != nil {
		serviceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) photo(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	data, err := s.photos.Read(name)
	if err != nil {
		serviceError(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", photos.ContentType(name))
	// A name is tied to one occurrence and never rewritten.
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	_, _ = w.Write(data)
}
