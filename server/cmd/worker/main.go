// Command worker drains the background job queue: media transcoding today,
// whatever else is queued later.
//
// It is a separate process from the API because transcoding is CPU-bound and
// must not compete with request handling. Scale it independently by running
// more copies; the queue claims with FOR UPDATE SKIP LOCKED, so several
// workers never take the same job.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/config"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/jobs"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/media"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/objstore"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/observability"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/postgres"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/ranking"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := observability.NewLogger(cfg.Env, os.Getenv("LOG_LEVEL"))
	slog.SetDefault(logger)

	pool, err := postgres.NewPool(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer pool.Close()

	blobs, err := objstore.New(cfg.Storage)
	if err != nil {
		// Unlike the API, the worker has nothing useful to do without storage.
		return err
	}

	// The worker id lands in jobs.locked_by, so a stuck job can be traced back
	// to the process that claimed it.
	id, _ := os.Hostname()
	if id == "" {
		id = "worker"
	}
	if pid := os.Getpid(); pid > 0 {
		id = id + "-" + itoa(pid)
	}

	queue := jobs.NewQueue(pool)
	runner := jobs.NewRunner(id, queue, logger)

	// Every kind of background work registers here. A kind with no handler is
	// failed rather than silently completed, so a deploy missing a worker shows
	// up in the dead letters instead of quietly losing work.
	mediaRepo := media.NewRepo(pool)
	runner.Handle(media.JobKindTranscode,
		media.NewWorker(mediaRepo, blobs, &media.FFmpegTranscoder{}, logger).Handler())

	// Nothing else in the service ever deletes from storage, so this is the
	// only thing standing between the store and paying for every byte forever.
	runner.Handle(media.JobKindGC, media.NewCollector(mediaRepo, blobs, logger).Handler())
	runner.Every(media.JobKindGC, 6*time.Hour, map[string]string{})

	// The leaderboard is recomputed daily, not hourly: a badge that flickers
	// between refreshes looks broken, and nothing about a 30-day window moves
	// fast enough to need more.
	rankingRepo := ranking.NewRepo(pool, logger)
	runner.Handle(ranking.JobKind, rankingRepo.Handler())
	runner.Every(ranking.JobKind, 24*time.Hour, map[string]string{})

	logger.Info("worker started", slog.String("id", id), slog.String("env", cfg.Env))
	if err := runner.Run(ctx); err != nil {
		return err
	}
	logger.Info("worker stopped cleanly")
	return nil
}

// itoa avoids pulling strconv in for one conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
