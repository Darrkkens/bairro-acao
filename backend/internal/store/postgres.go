// Package store keeps walks, occurrences and AI results in PostgreSQL.
package store

import (
	"bairroacao/internal/walk"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The AI's suggestion (ai_*) and the person's confirmation (category, title,
// description) live in separate columns so the model can be evaluated later.
const schema = `CREATE TABLE IF NOT EXISTS walks (
	id           uuid PRIMARY KEY,
	neighborhood text NOT NULL,
	started_at   timestamptz NOT NULL,
	finished_at  timestamptz,
	created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS occurrences (
	id             uuid PRIMARY KEY,
	walk_id        uuid NOT NULL REFERENCES walks (id) ON DELETE CASCADE,
	photo          text NOT NULL,
	note           text NOT NULL DEFAULT '',
	latitude       double precision,
	longitude      double precision,
	accuracy_m     double precision,
	captured_at    timestamptz NOT NULL,
	ai_status      text NOT NULL DEFAULT 'pending' CHECK (ai_status IN ('pending', 'running', 'done', 'needs_info', 'failed')),
	ai_category    text,
	ai_title       text,
	ai_description text,
	ai_confidence  text,
	ai_question    text,
	ai_model       text,
	ai_error       text,
	ai_analyzed_at timestamptz,
	category       text,
	title          text NOT NULL DEFAULT '',
	description    text NOT NULL DEFAULT '',
	reviewed_at    timestamptz,
	created_at     timestamptz NOT NULL DEFAULT now(),
	CHECK ((latitude IS NULL) = (longitude IS NULL))
);
CREATE INDEX IF NOT EXISTS occurrences_walk ON occurrences (walk_id, captured_at);
CREATE INDEX IF NOT EXISTS occurrences_queue ON occurrences (created_at) WHERE ai_status = 'pending';
ALTER TABLE walks ADD COLUMN IF NOT EXISTS city text NOT NULL DEFAULT '';
ALTER TABLE walks ADD COLUMN IF NOT EXISTS state text NOT NULL DEFAULT '';
-- GPS route recorded by the phone; keyed by time so a retried upload adds nothing.
CREATE TABLE IF NOT EXISTS track_points (
	walk_id     uuid NOT NULL REFERENCES walks (id) ON DELETE CASCADE,
	recorded_at timestamptz NOT NULL,
	latitude    double precision NOT NULL,
	longitude   double precision NOT NULL,
	accuracy_m  double precision,
	PRIMARY KEY (walk_id, recorded_at)
);
-- Grouping: extra photos of a point point to it; photo_signature feeds duplicate and similarity checks.
ALTER TABLE occurrences ADD COLUMN IF NOT EXISTS photo_signature bytea;
ALTER TABLE occurrences ADD COLUMN IF NOT EXISTS group_id uuid REFERENCES occurrences (id) ON DELETE CASCADE;
ALTER TABLE occurrences ADD COLUMN IF NOT EXISTS duplicate boolean NOT NULL DEFAULT false;
ALTER TABLE occurrences ADD COLUMN IF NOT EXISTS keep_separate boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS occurrences_group ON occurrences (group_id) WHERE group_id IS NOT NULL;
ALTER TABLE occurrences DROP CONSTRAINT IF EXISTS occurrences_ai_status_check;
ALTER TABLE occurrences ADD CONSTRAINT occurrences_ai_status_check CHECK (ai_status IN ('pending', 'running', 'done', 'needs_info', 'failed', 'grouped'));
-- Neighborhood lists from OpenStreetMap, by IBGE municipality code.
CREATE TABLE IF NOT EXISTS neighborhood_lists (
	ibge_code  text PRIMARY KEY,
	names      jsonb NOT NULL,
	fetched_at timestamptz NOT NULL
);`

type Postgres struct{ pool *pgxpool.Pool }

func OpenPostgres(ctx context.Context, url string) (*Postgres, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, err
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

const walkColumns = `id::text, neighborhood, city, state, started_at, finished_at`

func scanWalk(row pgx.Row) (walk.Walk, error) {
	var w walk.Walk
	err := row.Scan(&w.ID, &w.Neighborhood, &w.City, &w.State, &w.StartedAt, &w.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return walk.Walk{}, walk.ErrNotFound
	}
	return w, err
}

func (p *Postgres) CreateWalk(ctx context.Context, w walk.Walk) (walk.Walk, bool, error) {
	created, err := scanWalk(p.pool.QueryRow(ctx, `INSERT INTO walks (id, neighborhood, city, state, started_at) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO NOTHING RETURNING `+walkColumns, w.ID, w.Neighborhood, w.City, w.State, w.StartedAt))
	if errors.Is(err, walk.ErrNotFound) {
		existing, err := p.Walk(ctx, w.ID)
		return existing, false, err
	}
	return created, err == nil, err
}

func (p *Postgres) Walk(ctx context.Context, id string) (walk.Walk, error) {
	return scanWalk(p.pool.QueryRow(ctx, `SELECT `+walkColumns+` FROM walks WHERE id = $1`, id))
}

func (p *Postgres) Walks(ctx context.Context, limit int) ([]walk.WalkSummary, error) {
	rows, err := p.pool.Query(ctx, `SELECT w.id::text, w.neighborhood, w.city, w.state, w.started_at, w.finished_at,
			count(o.id), count(o.reviewed_at)
		FROM walks w LEFT JOIN occurrences o ON o.walk_id = w.id
		GROUP BY w.id ORDER BY w.started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (walk.WalkSummary, error) {
		var s walk.WalkSummary
		err := row.Scan(&s.ID, &s.Neighborhood, &s.City, &s.State, &s.StartedAt, &s.FinishedAt, &s.Occurrences, &s.Reviewed)
		return s, err
	})
}

func (p *Postgres) FinishWalk(ctx context.Context, id string, at time.Time) (walk.Walk, error) {
	return scanWalk(p.pool.QueryRow(ctx, `UPDATE walks SET finished_at = COALESCE(finished_at, GREATEST($2, started_at))
		WHERE id = $1 RETURNING `+walkColumns, id, at))
}

func (p *Postgres) AddTrack(ctx context.Context, walkID string, points []walk.TrackPoint) error {
	times := make([]time.Time, len(points))
	lats := make([]float64, len(points))
	lngs := make([]float64, len(points))
	accuracies := make([]*float64, len(points))
	for i, pt := range points {
		times[i], lats[i], lngs[i], accuracies[i] = pt.RecordedAt, pt.Latitude, pt.Longitude, pt.Accuracy
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO track_points (walk_id, recorded_at, latitude, longitude, accuracy_m)
		SELECT $1, * FROM unnest($2::timestamptz[], $3::float8[], $4::float8[], $5::float8[])
		ON CONFLICT DO NOTHING`, walkID, times, lats, lngs, accuracies)
	return err
}

func (p *Postgres) Track(ctx context.Context, walkID string) ([]walk.TrackPoint, error) {
	rows, err := p.pool.Query(ctx, `SELECT latitude, longitude, accuracy_m, recorded_at FROM track_points WHERE walk_id = $1 ORDER BY recorded_at`, walkID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (walk.TrackPoint, error) {
		var pt walk.TrackPoint
		err := row.Scan(&pt.Latitude, &pt.Longitude, &pt.Accuracy, &pt.RecordedAt)
		return pt, err
	})
}

func (p *Postgres) NeighborhoodList(ctx context.Context, code string) ([]string, time.Time, bool, error) {
	var names []string
	var at time.Time
	err := p.pool.QueryRow(ctx, `SELECT names, fetched_at FROM neighborhood_lists WHERE ibge_code = $1`, code).Scan(&names, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	return names, at, err == nil, err
}

func (p *Postgres) SaveNeighborhoodList(ctx context.Context, code string, names []string, at time.Time) error {
	if names == nil {
		names = []string{}
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO neighborhood_lists (ibge_code, names, fetched_at) VALUES ($1, $2, $3)
		ON CONFLICT (ibge_code) DO UPDATE SET names = EXCLUDED.names, fetched_at = EXCLUDED.fetched_at`, code, names, at)
	return err
}

const occurrenceColumns = `id::text, walk_id::text, photo, note, latitude, longitude, accuracy_m, captured_at,
	ai_status, ai_category, ai_title, ai_description, ai_confidence, ai_question, ai_model, ai_error,
	coalesce(category, ''), title, description, reviewed_at,
	coalesce(group_id::text, ''), duplicate, keep_separate, photo_signature`

func scanOccurrence(row pgx.Row) (walk.Occurrence, error) {
	var o walk.Occurrence
	var lat, lng, accuracy *float64
	var aiCategory, aiTitle, aiDescription, aiConfidence, aiQuestion, aiModel, aiError *string
	err := row.Scan(&o.ID, &o.WalkID, &o.Photo, &o.Note, &lat, &lng, &accuracy, &o.CapturedAt,
		&o.AIStatus, &aiCategory, &aiTitle, &aiDescription, &aiConfidence, &aiQuestion, &aiModel, &aiError,
		&o.Category, &o.Title, &o.Description, &o.ReviewedAt,
		&o.GroupID, &o.Duplicate, &o.KeepSeparate, &o.Signature)
	if errors.Is(err, pgx.ErrNoRows) {
		return walk.Occurrence{}, walk.ErrNotFound
	}
	if err != nil {
		return walk.Occurrence{}, err
	}
	if lat != nil && lng != nil {
		o.Location = &walk.Location{Latitude: *lat, Longitude: *lng, Accuracy: accuracy}
	}
	if aiCategory != nil && (o.AIStatus == walk.AIDone || o.AIStatus == walk.AINeedsInfo || o.AIStatus == walk.AIGrouped) {
		o.AI = &walk.Suggestion{Category: walk.Category(*aiCategory), Title: deref(aiTitle), Description: deref(aiDescription), Confidence: deref(aiConfidence), Question: deref(aiQuestion), Model: deref(aiModel)}
	}
	if o.AIStatus == walk.AIFailed {
		o.AIError = deref(aiError)
	}
	return o, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (p *Postgres) Occurrence(ctx context.Context, id string) (walk.Occurrence, error) {
	return scanOccurrence(p.pool.QueryRow(ctx, `SELECT `+occurrenceColumns+` FROM occurrences WHERE id = $1`, id))
}

func (p *Postgres) Occurrences(ctx context.Context, walkID string) ([]walk.Occurrence, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+occurrenceColumns+` FROM occurrences WHERE walk_id = $1 ORDER BY captured_at, id`, walkID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (walk.Occurrence, error) { return scanOccurrence(row) })
}

func (p *Postgres) AddOccurrence(ctx context.Context, o walk.Occurrence) (walk.Occurrence, bool, error) {
	var lat, lng, accuracy *float64
	if o.Location != nil {
		lat, lng, accuracy = &o.Location.Latitude, &o.Location.Longitude, o.Location.Accuracy
	}
	var groupID *string
	if o.GroupID != "" {
		groupID = &o.GroupID
	}
	status := o.AIStatus
	if status == "" {
		status = walk.AIPending
	}
	created, err := scanOccurrence(p.pool.QueryRow(ctx, `INSERT INTO occurrences (id, walk_id, photo, note, latitude, longitude, accuracy_m, captured_at, ai_status, group_id, duplicate, photo_signature)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) ON CONFLICT (id) DO NOTHING RETURNING `+occurrenceColumns,
		o.ID, o.WalkID, o.Photo, o.Note, lat, lng, accuracy, o.CapturedAt, string(status), groupID, o.Duplicate, o.Signature))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return walk.Occurrence{}, false, walk.ErrNotFound
	}
	if errors.Is(err, walk.ErrNotFound) {
		existing, err := p.Occurrence(ctx, o.ID)
		return existing, false, err
	}
	return created, err == nil, err
}

func (p *Postgres) Review(ctx context.Context, id string, r walk.Review, at time.Time) (walk.Occurrence, error) {
	return scanOccurrence(p.pool.QueryRow(ctx, `UPDATE occurrences SET category = $2, title = $3, description = $4, reviewed_at = $5
		WHERE id = $1 RETURNING `+occurrenceColumns, id, string(r.Category), r.Title, r.Description, at))
}

func (p *Postgres) Requeue(ctx context.Context, id, note string) (walk.Occurrence, error) {
	return scanOccurrence(p.pool.QueryRow(ctx, `UPDATE occurrences SET note = $2, ai_status = 'pending', ai_error = NULL
		WHERE id = $1 RETURNING `+occurrenceColumns, id, note))
}

func (p *Postgres) DeleteOccurrence(ctx context.Context, id string) ([]string, error) {
	rows, err := p.pool.Query(ctx, `DELETE FROM occurrences WHERE id = $1 OR group_id = $1 RETURNING photo`, id)
	if err != nil {
		return nil, err
	}
	photos, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err == nil && len(photos) == 0 {
		return nil, walk.ErrNotFound
	}
	return photos, err
}

// Group moves an occurrence, and any extra photos it had, under leader.
func (p *Postgres) Group(ctx context.Context, member, leader string) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE occurrences SET group_id = $2 WHERE group_id = $1`, member, leader); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE occurrences SET group_id = $2, ai_status = 'grouped', duplicate = false WHERE id = $1`, member, leader)
		return err
	})
}

// Ungroup returns whether the occurrence went to the analysis queue (it had never been analyzed).
func (p *Postgres) Ungroup(ctx context.Context, id string) (bool, error) {
	var status string
	err := p.pool.QueryRow(ctx, `UPDATE occurrences SET group_id = NULL, duplicate = false, keep_separate = true,
			ai_status = CASE WHEN ai_category IS NULL THEN 'pending' ELSE 'done' END
		WHERE id = $1 RETURNING ai_status`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, walk.ErrNotFound
	}
	return status == string(walk.AIPending), err
}

func (p *Postgres) KeepSeparate(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE occurrences SET keep_separate = true WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return walk.ErrNotFound
	}
	return err
}

func (p *Postgres) MissingSignatures(ctx context.Context) ([]walk.Occurrence, error) {
	rows, err := p.pool.Query(ctx, `SELECT id::text, photo FROM occurrences WHERE photo_signature IS NULL`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (walk.Occurrence, error) {
		var o walk.Occurrence
		err := row.Scan(&o.ID, &o.Photo)
		return o, err
	})
}

func (p *Postgres) SaveSignature(ctx context.Context, id string, signature []byte) error {
	_, err := p.pool.Exec(ctx, `UPDATE occurrences SET photo_signature = $2 WHERE id = $1`, id, signature)
	return err
}

// ClaimNext marks the oldest pending occurrence as running. SKIP LOCKED keeps
// it safe if more than one worker is ever started.
func (p *Postgres) ClaimNext(ctx context.Context) (walk.Job, bool, error) {
	var job walk.Job
	err := p.pool.QueryRow(ctx, `WITH next AS (
			SELECT id FROM occurrences WHERE ai_status = 'pending' ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		UPDATE occurrences o SET ai_status = 'running' FROM next, walks w
		WHERE o.id = next.id AND w.id = o.walk_id
		RETURNING o.id::text, o.photo, o.note, w.neighborhood`).Scan(&job.ID, &job.Photo, &job.Note, &job.Neighborhood)
	if errors.Is(err, pgx.ErrNoRows) {
		return walk.Job{}, false, nil
	}
	return job, err == nil, err
}

// CompleteAnalysis only applies to a running job, so a result that arrives
// after the person asked for a new analysis is dropped.
func (p *Postgres) CompleteAnalysis(ctx context.Context, id string, status walk.AIStatus, s walk.Suggestion, at time.Time) error {
	_, err := p.pool.Exec(ctx, `UPDATE occurrences SET ai_status = $2, ai_category = $3, ai_title = $4, ai_description = $5,
			ai_confidence = $6, ai_question = $7, ai_model = $8, ai_error = NULL, ai_analyzed_at = $9
		WHERE id = $1 AND ai_status = 'running'`,
		id, string(status), string(s.Category), s.Title, s.Description, s.Confidence, s.Question, s.Model, at)
	return err
}

func (p *Postgres) FailAnalysis(ctx context.Context, id, message string) error {
	_, err := p.pool.Exec(ctx, `UPDATE occurrences SET ai_status = 'failed', ai_error = $2 WHERE id = $1 AND ai_status = 'running'`, id, message)
	return err
}

// ReleaseAnalysis returns a running job to the queue, e.g. while Ollama is down.
func (p *Postgres) ReleaseAnalysis(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `UPDATE occurrences SET ai_status = 'pending' WHERE id = $1 AND ai_status = 'running'`, id)
	return err
}

// ResetRunning requeues jobs interrupted by a restart.
func (p *Postgres) ResetRunning(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, `UPDATE occurrences SET ai_status = 'pending' WHERE ai_status = 'running'`)
	return err
}
