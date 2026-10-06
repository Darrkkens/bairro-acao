package walk

import (
	"bairroacao/internal/similarity"
	"context"
	"errors"
	"log/slog"
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
	// DeleteOccurrence removes a point with its extra photos and returns their file names.
	DeleteOccurrence(context.Context, string) ([]string, error)
	Group(ctx context.Context, member, leader string) error
	Ungroup(context.Context, string) (bool, error)
	KeepSeparate(context.Context, string) error
	MissingSignatures(context.Context) ([]Occurrence, error)
	SaveSignature(context.Context, string, []byte) error
}

// Job is an occurrence claimed by the analysis worker.
type Job struct{ ID, Photo, Note, Neighborhood string }

// PhotoStore keeps the image files; the database stores only their names.
type PhotoStore interface {
	Save(name string, data []byte) error
	Read(name string) ([]byte, error)
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
	suggestGroups(occurrences)
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
	o := Occurrence{ID: id, WalkID: walkID, Photo: photo, Note: note, Location: in.Location, CapturedAt: in.CapturedAt.UTC(), AIStatus: AIPending}
	// The same shot sent twice (or a burst) joins the earlier point and skips the AI.
	if sig, err := similarity.Compute(in.Photo); err == nil {
		o.Signature, _ = sig.MarshalBinary()
		if existing, err := s.Repo.Occurrences(ctx, walkID); err == nil {
			if leader, ok := duplicateOf(sig, o.CapturedAt, existing); ok {
				o.GroupID, o.Duplicate, o.AIStatus = leader, true, AIGrouped
			}
		}
	}
	saved, created, err := s.Repo.AddOccurrence(ctx, o)
	if err != nil {
		_ = s.Photos.Remove(photo)
		return Occurrence{}, false, err
	}
	if created && saved.AIStatus == AIPending {
		s.wake()
	}
	return saved, created, nil
}

// Group makes an occurrence an extra photo of another point. If the target is
// itself an extra photo, its point is used; extras of the moved occurrence follow it.
func (s *Service) Group(ctx context.Context, id, with string) error {
	id, ok := NormalizeID(id)
	with, withOK := NormalizeID(with)
	if !ok || !withOK {
		return ErrNotFound
	}
	member, err := s.Repo.Occurrence(ctx, id)
	if err != nil {
		return err
	}
	leader, err := s.Repo.Occurrence(ctx, with)
	if err != nil {
		return err
	}
	if leader.GroupID != "" {
		if leader, err = s.Repo.Occurrence(ctx, leader.GroupID); err != nil {
			return err
		}
	}
	if leader.WalkID != member.WalkID || leader.ID == member.ID {
		return invalid("Só dá para agrupar pontos diferentes da mesma caminhada.")
	}
	return s.Repo.Group(ctx, member.ID, leader.ID)
}

// Ungroup makes an extra photo a point of its own again, analyzed if it never was.
func (s *Service) Ungroup(ctx context.Context, id string) error {
	id, ok := NormalizeID(id)
	if !ok {
		return ErrNotFound
	}
	o, err := s.Repo.Occurrence(ctx, id)
	if err != nil {
		return err
	}
	if o.GroupID == "" {
		return invalid("Esta foto já é um ponto separado.")
	}
	queued, err := s.Repo.Ungroup(ctx, id)
	if err == nil && queued {
		s.wake()
	}
	return err
}

// KeepSeparate records that a suggested grouping was wrong, so it is not offered again.
func (s *Service) KeepSeparate(ctx context.Context, id string) error {
	id, ok := NormalizeID(id)
	if !ok {
		return ErrNotFound
	}
	return s.Repo.KeepSeparate(ctx, id)
}

// BackfillSignatures describes photos stored before grouping existed.
func (s *Service) BackfillSignatures(ctx context.Context) {
	missing, err := s.Repo.MissingSignatures(ctx)
	if err != nil {
		slog.Warn("listing photos without signature failed", "error", err)
		return
	}
	for _, o := range missing {
		data, err := s.Photos.Read(o.Photo)
		if err != nil {
			continue
		}
		sig, err := similarity.Compute(data)
		if err != nil {
			continue
		}
		raw, _ := sig.MarshalBinary()
		if err := s.Repo.SaveSignature(ctx, o.ID, raw); err != nil {
			slog.Warn("saving photo signature failed", "occurrence", o.ID, "error", err)
		}
	}
	if len(missing) > 0 {
		slog.Info("photo signatures computed", "count", len(missing))
	}
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
	photos, err := s.Repo.DeleteOccurrence(ctx, id)
	if err != nil {
		return err
	}
	for _, photo := range photos {
		if err := s.Photos.Remove(photo); err != nil {
			return err
		}
	}
	return nil
}
