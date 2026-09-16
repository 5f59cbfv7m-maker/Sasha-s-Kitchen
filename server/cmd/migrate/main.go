// Command migrate applies the database schema and exits. It is a separate
// binary so a deploy can run migrations as its own step rather than racing
// several API replicas at startup.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/config"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/observability"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/postgres"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	logger := observability.NewLogger(cfg.Env, os.Getenv("LOG_LEVEL"))
	if err != nil {
		logger.Error("config", slog.Any("error", err))
		os.Exit(1)
	}

	pool, err := postgres.NewPool(ctx, cfg.Postgres)
	if err != nil {
		logger.Error("database", slog.Any("error", err))
		os.Exit(1)
	}
	defer pool.Close()

	if err := postgres.Migrate(ctx, pool, logger); err != nil {
		logger.Error("migrate", slog.Any("error", err))
		os.Exit(1)
	}
}
