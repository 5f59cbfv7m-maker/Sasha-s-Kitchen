// Command api serves the recipe store's public HTTP API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/bundle"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/cache"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/config"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/observability"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/postgres"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/storefront"
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

	// Migrations run as their own deploy step (cmd/migrate). Running them from
	// the API would race across replicas and slow every restart.
	if strings.EqualFold(os.Getenv("MIGRATE_ON_BOOT"), "true") {
		if err := postgres.Migrate(ctx, pool, logger); err != nil {
			return err
		}
	}

	redis := cache.New(ctx, cfg.Redis)
	media := cdnResolver{base: strings.TrimRight(cfg.Storage.PublicCDN, "/")}

	searchRepo := search.NewRepo(pool, media)
	storeRepo := storefront.NewRepo(pool, searchRepo, media)
	bundleRepo := bundle.NewRepo(pool)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Readiness checks the dependency the process cannot serve without, so a
	// replica with a broken pool is pulled from the load balancer.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			httpx.Respond(w, r, httpx.Unavailable("База данных недоступна").WithCause(err))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	// TODO(auth): replace with auth.UserFrom once the auth package lands. Until
	// then every caller is anonymous, which is correct for a read-only preview.
	viewer := func(*http.Request) string { return "" }

	api := storefront.NewAPI(pool, searchRepo, storeRepo, bundleRepo, redis, viewer, cfg.FeedCacheTTL)
	api.Routes(mux)

	handler := httpx.Chain(mux,
		httpx.RequestID(),
		httpx.Recover(logger),
		httpx.AccessLog(logger),
		httpx.SecurityHeaders(),
		httpx.CORS(strings.Split(os.Getenv("CORS_ORIGINS"), ",")),
		httpx.RateLimitByIP(cfg.RateLimitRPS, cfg.RateLimitBurst, 10*time.Minute),
		httpx.Timeout(15*time.Second),
	)

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: handler,
		// ReadHeaderTimeout is the one that closes slowloris connections.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api listening",
			slog.String("addr", cfg.HTTPAddr), slog.String("env", cfg.Env))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	// Drain in-flight requests before exiting so a deploy does not return
	// errors to users already mid-request.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("stopped cleanly")
	return nil
}

// cdnResolver turns storage keys into public URLs. Media is always served by
// the CDN, never proxied through this API.
type cdnResolver struct{ base string }

func (c cdnResolver) PublicURL(key string) string {
	if key == "" {
		return ""
	}
	if c.base == "" {
		// Local development without a CDN configured.
		return "/media/" + key
	}
	return c.base + "/" + strings.TrimLeft(key, "/")
}
