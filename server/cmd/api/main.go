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

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/admin"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/auth"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/author"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/bundle"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/cache"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/comments"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/config"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/jobs"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/media"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/moderation"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/objstore"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/observability"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/postgres"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/posts"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/ranking"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/social"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/storefront"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/studio"
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

	// Object storage is optional in development: without S3 credentials the
	// API still serves the storefront, it just cannot accept uploads.
	blobs, err := objstore.New(cfg.Storage)
	if err != nil {
		logger.Warn("object storage unavailable; media uploads disabled",
			slog.Any("error", err))
		blobs = nil
	}

	var mediaURLs search.MediaURLResolver = cdnResolver{
		base: strings.TrimRight(cfg.Storage.PublicCDN, "/"),
	}
	if blobs != nil {
		mediaURLs = blobs
	}

	searchRepo := search.NewRepo(pool, mediaURLs)
	storeRepo := storefront.NewRepo(pool, searchRepo, mediaURLs)
	bundleRepo := bundle.NewRepo(pool)

	authRepo := auth.NewRepo(pool)
	authSvc := auth.NewService(authRepo, cfg.Auth, auth.ServiceOptions{})
	authenticator := auth.NewAuthenticator(authSvc.Tokens(), authRepo)
	authHandlers := auth.NewHandlers(authSvc, authenticator)

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

	// The storefront is browsable anonymously, so identity is attached when a
	// token is present and the request proceeds regardless when it is not.
	viewer := func(r *http.Request) string {
		if id, ok := auth.UserFrom(r.Context()); ok {
			return id.UserID.String()
		}
		return ""
	}

	// Uploads, unlike the storefront, must know exactly who is writing.
	caller := func(r *http.Request) (uuid.UUID, bool) {
		id, ok := auth.UserFrom(r.Context())
		if !ok {
			return uuid.UUID{}, false
		}
		return id.UserID, true
	}

	authHandlers.Routes(mux)
	api := storefront.NewAPI(pool, searchRepo, storeRepo, bundleRepo, redis, viewer, cfg.FeedCacheTTL)
	api.Routes(mux)

	// Reporting and blocking. The storefront has always filtered listings by
	// user_blocks; until now there was no way for anyone to create one.
	social.NewAPI(moderation.NewRepo(pool), caller).Routes(mux)

	// Author pages. The recipe shelf runs through searchRepo, so an author's
	// listing shares its filters, cursors and block rules with the storefront.
	postsRepo := posts.NewRepo(pool, searchRepo, mediaURLs)
	posts.NewAPI(postsRepo, viewer).Routes(mux)
	comments.NewAPI(comments.NewRepo(pool, mediaURLs), viewer).Routes(mux)
	rankingRepo := ranking.NewRepo(pool, logger)
	author.NewAPI(author.NewRepo(pool, searchRepo, mediaURLs), searchRepo, postsRepo,
		rankingRepo, mediaURLs, viewer).Routes(mux)

	// The author's own workspace: profile, drafts and publishing.
	modRepo := moderation.NewRepo(pool)
	studio.NewAPI(studio.NewRepo(pool, postsRepo, modRepo), caller).Routes(mux)

	// The moderator surface. internal/moderation has had these tools since the
	// first schema and registered no routes, so none of them could be reached.
	admin.NewAPI(modRepo, func(r *http.Request) (uuid.UUID, bool) {
		id, ok := auth.UserFrom(r.Context())
		if !ok || !id.IsAdmin {
			return uuid.UUID{}, false
		}
		return id.UserID, true
	}).Routes(mux)

	if blobs != nil {
		mediaRepo := media.NewRepo(pool)
		mediaAPI := media.NewAPI(
			media.NewService(mediaRepo, blobs, jobs.NewQueue(pool)), mediaRepo, blobs, caller)
		mediaAPI.Routes(mux)
	}

	handler := httpx.Chain(mux,
		httpx.RequestID(),
		// Optional auth runs outermost so every downstream handler sees an
		// identity when one was supplied, without ever rejecting an
		// anonymous caller.
		authenticator.Optional(),
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
