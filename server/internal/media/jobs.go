package media

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// JobKindTranscode is the jobs.kind value the worker in this package
// processes.
const JobKindTranscode = "media.transcode"

// Job is one claimed row from the jobs table.
type Job struct {
	ID          int64
	Kind        string
	Payload     json.RawMessage
	Attempts    int
	MaxAttempts int
}

// Queue is a reusable Postgres-backed job queue built directly on the jobs
// table (see migrations/0004_jobs.sql). It is deliberately generic — nothing
// here is specific to media — so any other workstream that needs durable
// background work can reuse it as-is.
//
// Postgres is the broker: Claim uses SELECT ... FOR UPDATE SKIP LOCKED inside
// a single statement, which Postgres executes atomically, so two workers
// calling Claim concurrently can never lock the same row.
type Queue struct {
	pool *pgxpool.Pool

	// BackoffBase and BackoffMax control Fail's retry delay: attempt N (1-based)
	// waits min(BackoffBase * 2^(N-1), BackoffMax). Exported so tests can shrink
	// them; production code can leave the zero value, which NewQueue fills in
	// with sane defaults.
	BackoffBase time.Duration
	BackoffMax  time.Duration
}

// NewQueue builds a Queue over an existing pool with production-sized backoff
// defaults (base 30s, capped at 30m).
func NewQueue(pool *pgxpool.Pool) *Queue {
	return &Queue{
		pool:        pool,
		BackoffBase: 30 * time.Second,
		BackoffMax:  30 * time.Minute,
	}
}

type enqueueParams struct {
	priority    int16
	maxAttempts int
	runAfter    time.Time
	dedupeKey   *string
}

// EnqueueOption customises a single Enqueue call.
type EnqueueOption func(*enqueueParams)

// WithDedupeKey makes Enqueue a no-op (returning the existing job's id) if a
// queued or running job with the same key already exists.
func WithDedupeKey(key string) EnqueueOption {
	return func(p *enqueueParams) { p.dedupeKey = &key }
}

// WithPriority sets the job's priority (lower claims first). Default 100,
// matching the table's own default.
func WithPriority(priority int16) EnqueueOption {
	return func(p *enqueueParams) { p.priority = priority }
}

// WithRunAfter delays the job until t. Default now.
func WithRunAfter(t time.Time) EnqueueOption {
	return func(p *enqueueParams) { p.runAfter = t }
}

// WithMaxAttempts overrides the default retry budget (5, matching the
// table's own default).
func WithMaxAttempts(n int) EnqueueOption {
	return func(p *enqueueParams) { p.maxAttempts = n }
}

// Enqueue inserts a queued job. If a dedupe key is given and a queued or
// running job with that key already exists (the partial unique index
// jobs_dedupe_key_idx), the existing job's id is returned instead of
// inserting a duplicate — enqueueing the same logical work twice is a no-op
// while the first copy is still outstanding.
func (q *Queue) Enqueue(ctx context.Context, kind string, payload any, opts ...EnqueueOption) (int64, error) {
	p := enqueueParams{priority: 100, maxAttempts: 5, runAfter: time.Now()}
	for _, opt := range opts {
		opt(&p)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("media: marshal job payload: %w", err)
	}

	// The DO UPDATE with a self-referential no-op assignment is the standard
	// way to make ON CONFLICT still RETURNING the pre-existing row: DO NOTHING
	// would silently return no row on a dedupe hit, leaving the caller with no
	// id to report. A NULL dedupe key never matches the partial index's
	// predicate, so this single query path also covers plain, non-deduped
	// enqueues without a conditional.
	row := q.pool.QueryRow(ctx, `
		INSERT INTO jobs (kind, payload, priority, max_attempts, run_after, dedupe_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL AND status IN ('queued', 'running')
		DO UPDATE SET updated_at = jobs.updated_at
		RETURNING id`,
		kind, body, p.priority, p.maxAttempts, p.runAfter, p.dedupeKey)

	var id int64
	if err := row.Scan(&id); err != nil {
		return 0, fmt.Errorf("media: enqueue %s: %w", kind, err)
	}
	return id, nil
}

// Claim locks up to n queued, due jobs for workerID and marks them running.
// FOR UPDATE SKIP LOCKED lives inside a single CTE+UPDATE statement, which is
// what makes it safe for many workers to call this concurrently: Postgres
// takes the row locks and performs the update atomically, so no two callers
// can ever walk away with the same row.
func (q *Queue) Claim(ctx context.Context, workerID string, n int) ([]Job, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := q.pool.Query(ctx, `
		WITH cte AS (
			SELECT id FROM jobs
			WHERE status = 'queued' AND run_after <= now()
			ORDER BY priority, run_after, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE jobs j
		SET status = 'running', locked_at = now(), locked_by = $2, attempts = attempts + 1
		FROM cte
		WHERE j.id = cte.id
		RETURNING j.id, j.kind, j.payload, j.attempts, j.max_attempts`,
		n, workerID)
	if err != nil {
		return nil, fmt.Errorf("media: claim: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempts, &j.MaxAttempts); err != nil {
			return nil, fmt.Errorf("media: claim scan: %w", err)
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media: claim iterate: %w", err)
	}
	return jobs, nil
}

// Complete marks a claimed job done.
func (q *Queue) Complete(ctx context.Context, id int64) error {
	_, err := q.pool.Exec(ctx, `
		UPDATE jobs SET status = 'done', finished_at = now(), locked_at = NULL, locked_by = NULL
		WHERE id = $1 AND status = 'running'`, id)
	if err != nil {
		return fmt.Errorf("media: complete job %d: %w", id, err)
	}
	return nil
}

// Fail records a failed attempt. If the job's attempts (already incremented
// by Claim) have reached its max_attempts, it is moved to 'dead' and left
// there; otherwise it is rescheduled with exponential backoff.
func (q *Queue) Fail(ctx context.Context, id int64, cause error) error {
	base, max := q.BackoffBase, q.BackoffMax
	if base <= 0 {
		base = 30 * time.Second
	}
	if max <= 0 {
		max = 30 * time.Minute
	}

	var attempts, maxAttempts int
	row := q.pool.QueryRow(ctx, `SELECT attempts, max_attempts FROM jobs WHERE id = $1`, id)
	if err := row.Scan(&attempts, &maxAttempts); err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("media: fail job %d: not found", id)
		}
		return fmt.Errorf("media: fail job %d: read attempts: %w", id, err)
	}

	// The retry timestamp is computed here, in Go, and passed as a plain
	// timestamptz rather than an interval: pgx only knows how to encode a Go
	// time.Duration into a Postgres interval through a pgtype.Interval value,
	// and there is no reason to pull that type in when "now + delay" is just
	// as correct computed client-side.
	nextRun := time.Now().Add(backoffFor(attempts, base, max))
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}

	_, err := q.pool.Exec(ctx, `
		UPDATE jobs SET
			status = CASE WHEN attempts >= max_attempts THEN 'dead' ELSE 'queued' END,
			last_error = $2,
			run_after = CASE WHEN attempts >= max_attempts THEN run_after ELSE $3::timestamptz END,
			locked_at = NULL,
			locked_by = NULL,
			finished_at = CASE WHEN attempts >= max_attempts THEN now() ELSE NULL END
		WHERE id = $1`,
		id, msg, nextRun)
	if err != nil {
		return fmt.Errorf("media: fail job %d: %w", id, err)
	}
	return nil
}

// backoffFor computes the retry delay for the attempt that just finished
// (1-based): base * 2^(attempt-1), capped at max.
func backoffFor(attempt int, base, max time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	// Cap the exponent so this never overflows before the min() with max.
	shift := attempt - 1
	if shift > 30 {
		shift = 30
	}
	d := time.Duration(float64(base) * math.Pow(2, float64(shift)))
	if d <= 0 || d > max {
		return max
	}
	return d
}

// SweepStale releases jobs stuck in 'running' whose lock has gone stale
// (their worker died mid-job) back to 'queued' so another worker can pick
// them up. It returns how many jobs were released.
func (q *Queue) SweepStale(ctx context.Context, staleAfter time.Duration) (int64, error) {
	cutoff := time.Now().Add(-staleAfter)
	tag, err := q.pool.Exec(ctx, `
		UPDATE jobs SET status = 'queued', locked_at = NULL, locked_by = NULL
		WHERE status = 'running' AND locked_at < $1::timestamptz`,
		cutoff)
	if err != nil {
		return 0, fmt.Errorf("media: sweep stale: %w", err)
	}
	return tag.RowsAffected(), nil
}
