package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/postgres"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping database-backed test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	target := strings.TrimPrefix(u.Path, "/") + "_jobs"

	admin := *u
	admin.Path = "/postgres"
	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		t.Fatalf("connect to maintenance database: %v", err)
	}
	defer conn.Close(context.WithoutCancel(ctx))
	if _, err := conn.Exec(ctx, `CREATE DATABASE "`+target+`"`); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("create database: %v", err)
	}

	out := *u
	out.Path = "/" + target
	pool, err := pgxpool.New(ctx, out.String())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := postgres.Migrate(ctx, pool, quiet); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM jobs`); err != nil {
		t.Fatalf("clear queue: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestRunnerDispatchesByKind(t *testing.T) {
	pool := testPool(t)
	q := NewQueue(pool)
	ctx := context.Background()

	var alpha, beta atomic.Int32
	r := NewRunner("test", q, quietLogger())
	r.Batch = 10
	r.Handle("test.alpha", func(context.Context, Job) error { alpha.Add(1); return nil })
	r.Handle("test.beta", func(context.Context, Job) error { beta.Add(1); return nil })

	for _, kind := range []string{"test.alpha", "test.alpha", "test.beta"} {
		if _, err := q.Enqueue(ctx, kind, map[string]string{}); err != nil {
			t.Fatalf("enqueue %s: %v", kind, err)
		}
	}
	if n, err := r.ProcessBatch(ctx); err != nil || n != 3 {
		t.Fatalf("ProcessBatch = %d, %v; want 3, nil", n, err)
	}
	if alpha.Load() != 2 || beta.Load() != 1 {
		t.Errorf("alpha=%d beta=%d, want 2 and 1", alpha.Load(), beta.Load())
	}
}

// TestUnknownKindFails is the regression test for the wall this registry
// replaced: the worker used to compare against one hard-coded kind and fail
// everything else. Failing is still the right answer for a kind nobody
// registered -- it means a deploy is missing a worker -- but it must be a
// failure, never a silent completion that loses the work.
func TestUnknownKindFails(t *testing.T) {
	pool := testPool(t)
	q := NewQueue(pool)
	ctx := context.Background()

	id, err := q.Enqueue(ctx, "test.nobody_handles_this", map[string]string{})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	r := NewRunner("test", q, quietLogger())
	if _, err := r.ProcessBatch(ctx); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}

	var status, lastErr string
	if err := pool.QueryRow(ctx,
		`SELECT status, coalesce(last_error,'') FROM jobs WHERE id=$1`, id).Scan(&status, &lastErr); err != nil {
		t.Fatalf("read job: %v", err)
	}
	if status == "done" {
		t.Fatal("unhandled job was completed silently; the work is now lost")
	}
	if !strings.Contains(lastErr, "no handler") {
		t.Errorf("last_error = %q, want it to name the missing handler", lastErr)
	}
}

func TestFailedJobRetriesThenDies(t *testing.T) {
	pool := testPool(t)
	q := NewQueue(pool)
	ctx := context.Background()

	id, err := q.Enqueue(ctx, "test.always_fails", map[string]string{}, WithMaxAttempts(2))
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	r := NewRunner("test", q, quietLogger())
	r.Handle("test.always_fails", func(context.Context, Job) error {
		return errors.New("boom")
	})

	for attempt := 1; attempt <= 2; attempt++ {
		// Retries are scheduled with backoff, so bring run_after forward
		// instead of sleeping through it.
		if _, err := pool.Exec(ctx, `UPDATE jobs SET run_after = now() WHERE id=$1`, id); err != nil {
			t.Fatalf("advance run_after: %v", err)
		}
		if _, err := r.ProcessBatch(ctx); err != nil {
			t.Fatalf("ProcessBatch: %v", err)
		}
	}

	var status string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT status, attempts FROM jobs WHERE id=$1`, id).Scan(&status, &attempts); err != nil {
		t.Fatalf("read job: %v", err)
	}
	if status != "dead" {
		t.Errorf("status after exhausting attempts = %q, want dead", status)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

// TestPeriodicEnqueueIsDeduped is what lets several workers each hold the same
// ticker without coordinating: they race to enqueue and exactly one wins.
func TestPeriodicEnqueueIsDeduped(t *testing.T) {
	pool := testPool(t)
	q := NewQueue(pool)
	ctx := context.Background()

	const kind = "test.periodic"
	mk := func(id string) *Runner {
		r := NewRunner(id, q, quietLogger())
		r.Every(kind, time.Hour, map[string]string{})
		// Every staggers the first run; pull it forward so the test is not
		// waiting on wall-clock time.
		r.periodic[0].next = time.Now().Add(-time.Second)
		return r
	}
	one, two := mk("worker-1"), mk("worker-2")
	one.enqueueDue(ctx)
	two.enqueueDue(ctx)

	var queued int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE kind=$1 AND status IN ('queued','running')`,
		kind).Scan(&queued); err != nil {
		t.Fatalf("count: %v", err)
	}
	if queued != 1 {
		t.Fatalf("two workers enqueued %d copies of the same periodic job, want 1", queued)
	}

	// Once the job is done the dedupe key frees up, so the next tick schedules
	// the following run rather than being suppressed forever.
	var ran atomic.Int32
	one.Handle(kind, func(context.Context, Job) error { ran.Add(1); return nil })
	if _, err := one.ProcessBatch(ctx); err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	one.periodic[0].next = time.Now().Add(-time.Second)
	one.enqueueDue(ctx)

	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE kind=$1 AND status IN ('queued','running')`,
		kind).Scan(&queued); err != nil {
		t.Fatalf("count: %v", err)
	}
	if queued != 1 {
		t.Errorf("after completion the next run was not scheduled (queued=%d)", queued)
	}
	if ran.Load() != 1 {
		t.Errorf("handler ran %d times, want 1", ran.Load())
	}
}

func TestDuplicateHandlerPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registering a kind twice did not panic; the second handler would silently win")
		}
	}()
	r := NewRunner("test", nil, quietLogger())
	r.Handle("test.dup", func(context.Context, Job) error { return nil })
	r.Handle("test.dup", func(context.Context, Job) error { return nil })
}
