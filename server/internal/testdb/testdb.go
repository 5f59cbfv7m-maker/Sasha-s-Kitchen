// Package testdb provisions a private database per test package.
//
// `go test ./...` runs packages in parallel, and these tests TRUNCATE to assert
// exact result sets. Sharing one database therefore makes the suite fail by
// timing: one package's truncate destroys another's fixtures mid-run. That
// happened once and cost an afternoon, so every package gets its own.
//
// It also applies migrations, which means a test package can never run against
// a schema older than the migration the change under test needs.
package testdb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/postgres"
)

// Pool returns a pool onto this package's own database, named after the base
// database plus suffix. It skips the test when DATABASE_URL is unset, so the
// suite still runs on a machine without Postgres.
func Pool(t *testing.T, suffix string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping database-backed test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	testDSN, err := provision(ctx, dsn, suffix)
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

// Truncate empties the named tables, restarting identities and cascading.
func Truncate(t *testing.T, pool *pgxpool.Pool, tables ...string) {
	t.Helper()
	if len(tables) == 0 {
		return
	}
	_, err := pool.Exec(context.Background(),
		"TRUNCATE "+strings.Join(tables, ", ")+" RESTART IDENTITY CASCADE")
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func provision(ctx context.Context, dsn, suffix string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	base := strings.TrimPrefix(u.Path, "/")
	if base == "" {
		return "", errors.New("DATABASE_URL has no database name")
	}
	target := base + "_" + suffix

	// CREATE DATABASE cannot run inside a transaction or from the database
	// being created, so it goes over a separate connection to the maintenance
	// database.
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
		// The name comes from our own DSN, never from user input, and pgx
		// cannot parameterise a DDL identifier.
		if _, err := conn.Exec(ctx, `CREATE DATABASE "`+target+`"`); err != nil {
			// A package running in parallel may have won the race; that is fine.
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
