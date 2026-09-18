package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/migrations"
)

// migrationAdvisoryLock is an arbitrary but fixed key. Every replica takes it
// before migrating, so a rolling deploy cannot run two migrators at once.
const migrationAdvisoryLock int64 = 8_314_552_071_004_311

var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

type migration struct {
	version  int
	name     string
	body     string
	checksum string
}

// Migrate applies every pending migration in version order, each in its own
// transaction. Already-applied files are verified against their recorded
// checksum: editing a shipped migration is a silent-corruption bug, so it is
// reported as an error rather than ignored.
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	all, err := loadMigrations()
	if err != nil {
		return err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationAdvisoryLock); err != nil {
		return fmt.Errorf("migrate: advisory lock: %w", err)
	}
	defer func() {
		// Best effort: the lock is also released when the session ends.
		_, _ = conn.Exec(context.WithoutCancel(ctx),
			`SELECT pg_advisory_unlock($1)`, migrationAdvisoryLock)
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    int PRIMARY KEY,
			name       text        NOT NULL,
			checksum   text        NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("migrate: ensure schema_migrations: %w", err)
	}

	applied := map[int]string{}
	rows, err := conn.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("migrate: read applied: %w", err)
	}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			rows.Close()
			return fmt.Errorf("migrate: scan applied: %w", err)
		}
		applied[v] = sum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("migrate: iterate applied: %w", err)
	}

	pending := 0
	for _, m := range all {
		if sum, ok := applied[m.version]; ok {
			if sum != m.checksum {
				return fmt.Errorf(
					"migrate: migration %04d_%s was modified after it was applied "+
						"(recorded %s, file %s); write a new migration instead",
					m.version, m.name, sum[:12], m.checksum[:12])
			}
			continue
		}

		if err := applyOne(ctx, conn.Conn(), m); err != nil {
			return err
		}
		pending++
		logger.Info("migration applied",
			slog.Int("version", m.version), slog.String("name", m.name))
	}

	if pending == 0 {
		logger.Info("migrations up to date", slog.Int("count", len(all)))
	}
	return nil
}

func applyOne(ctx context.Context, conn *pgx.Conn, m migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: begin %04d: %w", m.version, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, m.body); err != nil {
		return fmt.Errorf("migrate: apply %04d_%s: %w", m.version, m.name, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
		m.version, m.name, m.checksum); err != nil {
		return fmt.Errorf("migrate: record %04d: %w", m.version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: commit %04d: %w", m.version, err)
	}
	return nil
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("migrate: read embedded dir: %w", err)
	}

	var out []migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		match := migrationName.FindStringSubmatch(e.Name())
		if match == nil {
			return nil, fmt.Errorf(
				"migrate: %q does not match NNNN_lower_snake.sql", e.Name())
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("migrate: bad version in %q: %w", e.Name(), err)
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf(
				"migrate: duplicate version %04d in %q and %q", version, prev, e.Name())
		}
		seen[version] = e.Name()

		body, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			return nil, fmt.Errorf("migrate: read %q: %w", e.Name(), err)
		}
		sum := sha256.Sum256(body)
		out = append(out, migration{
			version:  version,
			name:     match[2],
			body:     string(body),
			checksum: hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}
