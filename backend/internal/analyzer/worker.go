// Package analyzer runs the AI over recorded occurrences in the background, one
// at a time, so recording a point during the walk never waits for inference.
package analyzer

import (
	"bairroacao/internal/ai"
	"bairroacao/internal/walk"
	"context"
	"errors"
	"log/slog"
	"time"
)

type Queue interface {
	ClaimNext(context.Context) (walk.Job, bool, error)
	CompleteAnalysis(context.Context, string, walk.AIStatus, walk.Suggestion, time.Time) error
	FailAnalysis(context.Context, string, string) error
	ReleaseAnalysis(context.Context, string) error
	ResetRunning(context.Context) error
}

type Model interface {
	Analyze(context.Context, ai.Request) (walk.Suggestion, walk.AIStatus, error)
}

type Photos interface {
	Read(string) ([]byte, error)
}

type Worker struct {
	queue  Queue
	model  Model
	photos Photos
	wake   chan struct{}
	// Retry is how long the worker waits before trying again when the model is unavailable.
	Retry time.Duration
}

func New(queue Queue, model Model, photos Photos) *Worker {
	return &Worker{queue: queue, model: model, photos: photos, wake: make(chan struct{}, 1), Retry: 30 * time.Second}
}

// Wake never blocks; several calls while the worker is busy collapse into one.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) Run(ctx context.Context) {
	if err := w.queue.ResetRunning(ctx); err != nil {
		slog.Error("analysis queue reset failed", "error", err)
	}
	for {
		for w.Step(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-time.After(w.Retry):
		}
	}
}

// Step analyzes the next pending occurrence and reports whether to continue
// immediately (false when the queue is empty or the model is unreachable).
func (w *Worker) Step(ctx context.Context) bool {
	job, ok, err := w.queue.ClaimNext(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("analysis queue unavailable", "error", err)
		}
		return false
	}
	if !ok {
		return false
	}
	image, err := w.photos.Read(job.Photo)
	if err != nil {
		slog.Error("occurrence photo unreadable", "occurrence", job.ID, "error", err)
		w.write(ctx, func(c context.Context) error {
			return w.queue.FailAnalysis(c, job.ID, "A foto deste registro não foi encontrada no servidor.")
		})
		return true
	}
	started := time.Now()
	suggestion, status, err := w.model.Analyze(ctx, ai.Request{Image: image, Neighborhood: job.Neighborhood, Note: job.Note})
	switch {
	case errors.Is(err, ai.ErrUnavailable) || ctx.Err() != nil:
		w.write(ctx, func(c context.Context) error { return w.queue.ReleaseAnalysis(c, job.ID) })
		if ctx.Err() == nil {
			slog.Warn("AI unavailable; occurrence back in queue", "occurrence", job.ID, "retry_in", w.Retry)
		}
		return false
	case err != nil:
		slog.Warn("AI analysis failed", "occurrence", job.ID, "error", err)
		w.write(ctx, func(c context.Context) error { return w.queue.FailAnalysis(c, job.ID, err.Error()) })
		return true
	}
	if !w.write(ctx, func(c context.Context) error {
		return w.queue.CompleteAnalysis(c, job.ID, status, suggestion, time.Now())
	}) {
		w.write(ctx, func(c context.Context) error { return w.queue.ReleaseAnalysis(c, job.ID) })
		return false
	}
	slog.Info("occurrence analyzed", "occurrence", job.ID, "status", status, "category", suggestion.Category, "confidence", suggestion.Confidence, "took", time.Since(started).Round(time.Millisecond))
	return true
}

// write runs one queue update with its own deadline; it outlives a shutdown
// so an interrupted job is not left marked as running.
func (w *Worker) write(ctx context.Context, update func(context.Context) error) bool {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := update(c); err != nil {
		slog.Error("analysis queue update failed", "error", err)
		return false
	}
	return true
}
