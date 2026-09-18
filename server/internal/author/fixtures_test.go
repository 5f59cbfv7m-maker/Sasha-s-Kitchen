package author

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/postgres"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
)

// testPool gives this package its own database, as internal/search does: these
// tests TRUNCATE, and `go test ./...` runs packages in parallel.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping database-backed test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	testDSN, err := provisionDB(ctx, dsn, "author")
	if err != nil {
		t.Fatalf("provision test database: %v", err)
	}
	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func provisionDB(ctx context.Context, dsn, suffix string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	base := strings.TrimPrefix(u.Path, "/")
	if base == "" {
		return "", errors.New("DATABASE_URL has no database name")
	}
	target := base + "_" + suffix

	admin := *u
	admin.Path = "/postgres"
	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		return "", fmt.Errorf("connect to maintenance database: %w", err)
	}
	defer conn.Close(context.WithoutCancel(ctx))

	var exists bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, target).Scan(&exists); err != nil {
		return "", fmt.Errorf("check database: %w", err)
	}
	if !exists {
		if _, err := conn.Exec(ctx, `CREATE DATABASE "`+target+`"`); err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				return "", fmt.Errorf("create database: %w", err)
			}
		}
	}
	out := *u
	out.Path = "/" + target
	testDSN := out.String()

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		return "", fmt.Errorf("connect to test database: %w", err)
	}
	defer pool.Close()
	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := postgres.Migrate(ctx, pool, quiet); err != nil {
		return "", fmt.Errorf("migrate test database: %w", err)
	}
	return testDSN, nil
}

// stubResolver stands in for the CDN, as in internal/search.
type stubResolver struct{}

func (stubResolver) PublicURL(key string) string { return "https://cdn.test/" + key }

type rig struct {
	pool   *pgxpool.Pool
	repo   *Repo
	search *search.Repo
	anna   string // author with recipes
	boris  string // another author
	reader string // plain reader
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		TRUNCATE users, recipes, media_assets, recipe_media, follows,
		         user_blocks, user_handle_history, user_stats
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	searchRepo := search.NewRepo(pool, stubResolver{})
	r := &rig{pool: pool, search: searchRepo,
		repo: NewRepo(pool, searchRepo, stubResolver{})}

	r.anna = r.user(t, "chef_anna", "Анна", true)
	r.boris = r.user(t, "chef_boris", "Борис", true)
	r.reader = r.user(t, "just_reader", "Читатель", false)

	for i, title := range []string{"Борщ", "Блины", "Омлет"} {
		r.recipe(t, r.anna, fmt.Sprintf("anna-%d", i), title)
	}
	r.recipe(t, r.boris, "boris-0", "Тирамису")
	return r
}

func (r *rig) user(t *testing.T, handle, name string, isAuthor bool) string {
	t.Helper()
	var id string
	if err := r.pool.QueryRow(context.Background(), `
		INSERT INTO users (handle, display_name, is_author, trust_level)
		VALUES ($1, $2, $3, 1) RETURNING id::text`, handle, name, isAuthor).Scan(&id); err != nil {
		t.Fatalf("create user %s: %v", handle, err)
	}
	return id
}

func (r *rig) recipe(t *testing.T, authorID, slug, title string) string {
	t.Helper()
	var id string
	if err := r.pool.QueryRow(context.Background(), `
		INSERT INTO recipes (author_id, slug, title, status, published_at)
		VALUES ($1::uuid, $2, $3, 'published', now() - make_interval(mins => $4))
		RETURNING id::text`, authorID, slug, title, int32(len(slug))).Scan(&id); err != nil {
		t.Fatalf("create recipe %s: %v", slug, err)
	}
	return id
}

func (r *rig) handler(viewer string) http.Handler {
	mux := http.NewServeMux()
	NewAPI(r.repo, r.search, func(*http.Request) string { return viewer }).Routes(mux)
	return mux
}

func do(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return id
}
