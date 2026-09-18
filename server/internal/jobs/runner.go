package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Handler runs one job. Returning an error fails the job, which the queue
// retries with backoff until max_attempts is spent.
//
// A handler must be idempotent. The queue guarantees at-least-once delivery,
// never exactly-once: a worker killed between finishing the work and marking
// the job done will see that job again.
type Handler func(ctx context.Context, job Job) error

// Runner claims jobs and dispatches them to the handler registered for their
// kind.
//
// The registry replaced a single hard-coded comparison against the transcode
// kind, which failed every other kind outright. That was fine while one kind
// existed and became a wall the moment a second one did.
type Runner struct {
	id       string
	queue    *Queue
	handlers map[string]Handler
	periodic []periodic
	logger   *slog.Logger

	// Batch is how many jobs to claim per poll; PollInterval is the idle wait.
	Batch        int
	PollInterval time.Duration
	// StaleAfter is how long a claimed job may sit before the sweeper frees it,
	// which is how a worker killed mid-job releases its work.
	StaleAfter time.Duration
}

type periodic struct {
	kind    string
	every   time.Duration
	payload any
	next    time.Time
}

func NewRunner(id string, queue *Queue, logger *slog.Logger) *Runner {
	return &Runner{
		id: id, queue: queue, logger: logger,
		handlers:     map[string]Handler{},
		Batch:        2,
		PollInterval: 2 * time.Second,
		StaleAfter:   30 * time.Minute,
	}
}

// Handle registers the handler for a job kind. Registering a kind twice is a
// programming error and panics at wiring time rather than dropping work later.
func (r *Runner) Handle(kind string, h Handler) {
	if _, dup := r.handlers[kind]; dup {
		panic("jobs: duplicate handler for kind " + kind)
	}
	r.handlers[kind] = h
}

// Every schedules recurring work.
//
// Enqueueing carries the kind as its dedupe key, and the partial unique index
// on jobs makes a second copy a no-op while the first is still queued or
// running. That is what lets several workers each hold this ticker without
// coordinating: they race to enqueue and exactly one wins.
func (r *Runner) Every(kind string, every time.Duration, payload any) {
	r.periodic = append(r.periodic, periodic{
		kind: kind, every: every, payload: payload,
		// Stagger the first run so a fleet restart does not fire everything at
		// once against a cold database.
		next: time.Now().Add(every / 10),
	})
}

// Run drains the queue until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	for kind := range r.handlers {
		r.logger.Info("job handler registered", slog.String("kind", kind))
	}

	sweep := time.NewTicker(r.StaleAfter / 2)
	defer sweep.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sweep.C:
			if n, err := r.queue.SweepStale(ctx, r.StaleAfter); err != nil {
				r.logger.Error("sweep stale jobs", slog.Any("error", err))
			} else if n > 0 {
				r.logger.Warn("released stale jobs", slog.Int64("count", n))
			}
		default:
		}

		r.enqueueDue(ctx)

		n, err := r.ProcessBatch(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			r.logger.Error("process batch", slog.Any("error", err))
		}
		if n == 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(r.PollInterval):
			}
		}
	}
}

// enqueueDue puts any due recurring work on the queue.
func (r *Runner) enqueueDue(ctx context.Context) {
	now := time.Now()
	for i := range r.periodic {
		p := &r.periodic[i]
		if now.Before(p.next) {
			continue
		}
		p.next = now.Add(p.every)
		if _, err := r.queue.Enqueue(ctx, p.kind, p.payload, WithDedupeKey(p.kind)); err != nil {
			r.logger.Error("enqueue periodic job",
				slog.String("kind", p.kind), slog.Any("error", err))
		}
	}
}

// ProcessBatch claims and runs up to Batch jobs, returning how many ran.
func (r *Runner) ProcessBatch(ctx context.Context) (int, error) {
	claimed, err := r.queue.Claim(ctx, r.id, r.Batch)
	if err != nil {
		return 0, err
	}
	for _, job := range claimed {
		r.run(ctx, job)
	}
	return len(claimed), nil
}

func (r *Runner) run(ctx context.Context, job Job) {
	log := r.logger.With(slog.Int64("job_id", job.ID), slog.String("kind", job.Kind))

	h, ok := r.handlers[job.Kind]
	if !ok {
		// Failing is better than silently completing: an unhandled kind means a
		// deploy is missing a worker, and that should be visible in the dead
		// letters rather than swallowed.
		_ = r.queue.Fail(ctx, job.ID, fmt.Errorf("jobs: no handler for kind %q", job.Kind))
		return
	}

	if err := h(ctx, job); err != nil {
		log.Error("job failed", slog.Any("error", err))
		_ = r.queue.Fail(ctx, job.ID, err)
		return
	}
	if err := r.queue.Complete(ctx, job.ID); err != nil {
		log.Error("complete job", slog.Any("error", err))
	}
}
