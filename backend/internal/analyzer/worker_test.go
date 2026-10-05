package analyzer

import (
	"bairroacao/internal/ai"
	"bairroacao/internal/walk"
	"context"
	"errors"
	"testing"
	"time"
)

type fakeQueue struct {
	jobs                        []walk.Job
	completed, failed, released []string
}

func (q *fakeQueue) ClaimNext(context.Context) (walk.Job, bool, error) {
	if len(q.jobs) == 0 {
		return walk.Job{}, false, nil
	}
	job := q.jobs[0]
	q.jobs = q.jobs[1:]
	return job, true, nil
}
func (q *fakeQueue) CompleteAnalysis(_ context.Context, id string, _ walk.AIStatus, _ walk.Suggestion, _ time.Time) error {
	q.completed = append(q.completed, id)
	return nil
}
func (q *fakeQueue) FailAnalysis(_ context.Context, id, _ string) error {
	q.failed = append(q.failed, id)
	return nil
}
func (q *fakeQueue) ReleaseAnalysis(_ context.Context, id string) error {
	q.released = append(q.released, id)
	return nil
}
func (q *fakeQueue) ResetRunning(context.Context) error { return nil }

type fakeModel struct{ err error }

func (m fakeModel) Analyze(context.Context, ai.Request) (walk.Suggestion, walk.AIStatus, error) {
	if m.err != nil {
		return walk.Suggestion{}, "", m.err
	}
	return walk.Suggestion{Category: walk.Street}, walk.AIDone, nil
}

type fakePhotos struct{}

func (fakePhotos) Read(name string) ([]byte, error) {
	if name == "missing.jpg" {
		return nil, errors.New("gone")
	}
	return []byte{1}, nil
}

func TestStepOutcomes(t *testing.T) {
	cases := []struct {
		name                        string
		photo                       string
		err                         error
		more                        bool
		completed, failed, released int
	}{
		{"analyzed", "a.jpg", nil, true, 1, 0, 0},
		{"model down keeps the job queued", "a.jpg", ai.ErrUnavailable, false, 0, 0, 1},
		{"bad answer fails the job", "a.jpg", ai.ErrInvalidResponse, true, 0, 1, 0},
		{"missing photo fails the job", "missing.jpg", nil, true, 0, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := &fakeQueue{jobs: []walk.Job{{ID: "1", Photo: c.photo}}}
			w := New(q, fakeModel{c.err}, fakePhotos{})
			if more := w.Step(context.Background()); more != c.more {
				t.Errorf("Step() = %v", more)
			}
			if len(q.completed) != c.completed || len(q.failed) != c.failed || len(q.released) != c.released {
				t.Errorf("completed %v failed %v released %v", q.completed, q.failed, q.released)
			}
		})
	}
}

func TestWakeNeverBlocks(t *testing.T) {
	w := New(&fakeQueue{}, fakeModel{}, fakePhotos{})
	for range 5 {
		w.Wake()
	}
}
