package walk

import (
	"context"
	"errors"
	"time"
)

// Repository persists walks and occurrences (see internal/store).
type Repository interface {
	CreateWalk(context.Context, Walk) (Walk, bool, error)
	Walk(context.Context, string) (Walk, error)
	Walks(context.Context, int) ([]WalkSummary, error)
	FinishWalk(context.Context, string, time.Time) (Walk, error)
	AddTrack(context.Context, string, []TrackPoint) error
	Track(context.Context, string) ([]TrackPoint, error)
	Occurrence(context.Context, string) (Occurrence, error)
	Occurrences(context.Context, string) ([]Occurrence, error)
	AddOccurrence(context.Context, Occurrence) (Occurrence, bool, error)
	Review(context.Context, string, Review, time.Time) (Occurrence, error)
	Requeue(context.Context, string, string) (Occurrence, error)
	DeleteOccurrence(context.Context, string) (string, error)
}

// Job is an occurrence claimed by the analysis worker.
type Job struct{ ID, Photo, Note, Neighborhood string }

// PhotoStore keeps the image files; the database stores only their names.
type PhotoStore interface {
	Save(name string, data []byte) error
	Remove(name string) error
}

type Service struct {
	Repo   Repository
	Photos PhotoStore
	// Wake tells the analysis worker that an occurrence is waiting.
	Wake func()
	Now  func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) wake() {
	if s.Wake != nil {
		s.Wake()
	}
}

// StartWalk is idempotent: the app creates the ID offline and may retry the upload.
func (s *Service) StartWalk(ctx context.Context, in Walk) (Walk, bool, error) {
	id, ok := NormalizeID(in.ID)
	if !ok {
		return Walk{}, false, invalid("Identificador de caminhada inválido.")
	}
	neighborhood, err := NormalizeNeighborhood(in.Neighborhood)
	if err != nil {
		return Walk{}, false, err
	}
	city, err := NormalizeCity(in.City)
	if err != nil {
		return Walk{}, false, err
	}
	state, err := NormalizeState(in.State)
	if err != nil {
		return Walk{}, false, err
	}
	if err := ValidateTime(in.StartedAt, s.now()); err != nil {
		return Walk{}, false, err
	}
	return s.Repo.CreateWalk(ctx, Walk{ID: id, Neighborhood: neighborhood, City: city, State: state, StartedAt: in.StartedAt.UTC()})
}

func (s *Service) Walk(ctx context.Context, id string) (Walk, []Occurrence, error) {
	id, ok := NormalizeID(id)
	if !ok {
		return Walk{}, nil, ErrNotFound
	}
	w, err := s.Repo.Walk(ctx, id)
	if err != nil {
		return Walk{}, nil, err
	}
	occurrences, err := s.Repo.Occurrences(ctx, id)
	return w, occurrences, err
}

func (s *Service) Walks(ctx context.Context) ([]WalkSummary, error) {
	return s.Repo.Walks(ctx, 20)
}

// FinishWalk stores the route recorded on the phone and closes the walk.
// Retrying is safe: track points are keyed by time and the end time is kept.
func (s *Service) FinishWalk(ctx context.Context, id string, at time.Time, track []TrackPoint) (Walk, error) {
	id, ok := NormalizeID(id)
	if !ok {
		return Walk{}, ErrNotFound
	}
	if at.IsZero() {
		at = s.now()
	}
	if err := ValidateTime(at, s.now()); err != nil {
		return Walk{}, err
	}
	if _, err := s.Repo.Walk(ctx, id); err != nil {
		return Walk{}, err
	}
	if track = CleanTrack(track, s.now()); len(track) > 0 {
		if err := s.Repo.AddTrack(ctx, id, track); err != nil {
			return Walk{}, err
		}
	}
	return s.Repo.FinishWalk(ctx, id, at.UTC())
}

func (s *Service) Track(ctx context.Context, id string) ([]TrackPoint, error) {
	id, ok := NormalizeID(id)
	if !ok {
		return nil, ErrNotFound
	}
	if _, err := s.Repo.Walk(ctx, id); err != nil {
		return nil, err
	}
	return s.Repo.Track(ctx, id)
}

type NewOccurrence struct {
	ID, WalkID string
	Photo      []byte
	Note       string
	Location   *Location
	CapturedAt time.Time
}

// Record stores a photo taken during the walk and queues it for analysis.
// Retrying with the same ID returns the stored occurrence untouched.
func (s *Service) Record(ctx context.Context, in NewOccurrence) (Occurrence, bool, error) {
	id, ok := NormalizeID(in.ID)
	walkID, walkOK := NormalizeID(in.WalkID)
	if !ok || !walkOK {
		return Occurrence{}, false, invalid("Identificador de registro inválido.")
	}
	if existing, err := s.Repo.Occurrence(ctx, id); err == nil {
		if existing.WalkID != walkID {
			return Occurrence{}, false, invalid("Este registro pertence a outra caminhada.")
		}
		return existing, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Occurrence{}, false, err
	}
	kind, ok := PhotoKind(in.Photo)
	if !ok {
		return Occurrence{}, false, invalid("Envie uma foto em JPEG ou PNG.")
	}
	note, err := NormalizeNote(in.Note)
	if err != nil {
		return Occurrence{}, false, err
	}
	if err := ValidateLocation(in.Location); err != nil {
		return Occurrence{}, false, err
	}
	if err := ValidateTime(in.CapturedAt, s.now()); err != nil {
		return Occurrence{}, false, err
	}
	photo := id + "." + kind
	if err := s.Photos.Save(photo, in.Photo); err != nil {
		return Occurrence{}, false, err
	}
	o, created, err := s.Repo.AddOccurrence(ctx, Occurrence{ID: id, WalkID: walkID, Photo: photo, Note: note, Location: in.Location, CapturedAt: in.CapturedAt.UTC(), AIStatus: AIPending})
	if err != nil {
		_ = s.Photos.Remove(photo)
		return Occurrence{}, false, err
	}
	if created {
		s.wake()
	}
	return o, created, nil
}

func (s *Service) Review(ctx context.Context, id string, r Review) (Occurrence, error) {
	id, ok := NormalizeID(id)
	if !ok {
		return Occurrence{}, ErrNotFound
	}
	r, err := NormalizeReview(r)
	if err != nil {
		return Occurrence{}, err
	}
	return s.Repo.Review(ctx, id, r, s.now())
}

// Reanalyze queues the occurrence again, usually with the complementary
// description the AI asked for.
func (s *Service) Reanalyze(ctx context.Context, id, note string) (Occurrence, error) {
	id, ok := NormalizeID(id)
	if !ok {
		return Occurrence{}, ErrNotFound
	}
	note, err := NormalizeNote(note)
	if err != nil {
		return Occurrence{}, err
	}
	o, err := s.Repo.Requeue(ctx, id, note)
	if err == nil {
		s.wake()
	}
	return o, err
}

func (s *Service) Delete(ctx context.Context, id string) error {
	id, ok := NormalizeID(id)
	if !ok {
		return ErrNotFound
	}
	photo, err := s.Repo.DeleteOccurrence(ctx, id)
	if err != nil {
		return err
	}
	return s.Photos.Remove(photo)
}
