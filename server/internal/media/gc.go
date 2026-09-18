package media

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/jobs"
)

// JobKindGC is the jobs.kind for the sweep that finds unreferenced assets.
const JobKindGC = "media.gc"

// DefaultGrace is how long an asset must be unreferenced before it is
// collected.
//
// This is not a tuning knob, it is a correctness requirement. An asset is
// created 'pending' and attached to a post or recipe only after the client has
// finished uploading it, so an author who spends an afternoon writing an
// article has assets sitting unreferenced the whole time. A day of grace makes
// that safe; an hour would delete work in progress.
const DefaultGrace = 24 * time.Hour

// GCBatch bounds one sweep, so a backlog is worked through over several runs
// rather than in one long transaction holding storage calls open.
const GCBatch = 200

// Collector reclaims storage.
type Collector struct {
	repo   *Repo
	store  objectDeleter
	logger *slog.Logger

	// Grace overrides DefaultGrace; tests set it short.
	Grace time.Duration
}

// objectDeleter is the slice of the object store this needs. Narrow on purpose:
// a collector that could also write would be a worse thing to have a bug in.
type objectDeleter interface {
	Delete(ctx context.Context, key string) error
}

func NewCollector(repo *Repo, store objectDeleter, logger *slog.Logger) *Collector {
	return &Collector{repo: repo, store: store, logger: logger, Grace: DefaultGrace}
}

// Handler adapts the collector to the job runner.
func (c *Collector) Handler() jobs.Handler {
	return func(ctx context.Context, _ jobs.Job) error {
		n, err := c.Sweep(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			c.logger.Info("media collected", slog.Int("assets", n))
		}
		return nil
	}
}

// Orphan is an asset with nothing pointing at it.
type Orphan struct {
	ID   uuid.UUID
	Keys []string
}

// Sweep marks unreferenced assets and deletes their objects.
//
// Order matters: the row is marked 'orphaned' first, then the objects go, then
// the row goes. A crash between the steps leaves an asset marked orphaned whose
// objects may or may not exist, and the next sweep finishes the job -- whereas
// deleting the row first would lose the keys and leak the bytes forever.
func (c *Collector) Sweep(ctx context.Context) (int, error) {
	orphans, err := c.repo.MarkOrphaned(ctx, c.Grace, GCBatch)
	if err != nil {
		return 0, err
	}
	// Anything already marked by an interrupted run is finished here too.
	stale, err := c.repo.ListOrphaned(ctx, GCBatch)
	if err != nil {
		return 0, err
	}
	orphans = append(orphans, stale...)

	seen := map[uuid.UUID]bool{}
	deleted := 0
	for _, o := range orphans {
		if seen[o.ID] {
			continue
		}
		seen[o.ID] = true

		failed := false
		for _, key := range o.Keys {
			if key == "" {
				continue
			}
			if err := c.store.Delete(ctx, key); err != nil {
				// Leave the row marked so the next sweep retries. Dropping it
				// now would orphan the bytes with no record of the key.
				c.logger.Warn("delete media object",
					slog.String("key", key), slog.Any("error", err))
				failed = true
			}
		}
		if failed {
			continue
		}
		if err := c.repo.DeleteAsset(ctx, o.ID); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

// MarkOrphaned flags unreferenced assets and returns their storage keys.
func (r *Repo) MarkOrphaned(ctx context.Context, grace time.Duration, limit int) ([]Orphan, error) {
	rows, err := r.pool.Query(ctx, `
		UPDATE media_assets m SET status = 'orphaned'
		 WHERE m.id IN (SELECT id FROM sk_media_unreferenced($1) LIMIT $2)
		RETURNING m.id, coalesce(m.storage_key,''), coalesce(m.poster_key,''), coalesce(m.hls_key,'')`,
		grace, limit)
	if err != nil {
		return nil, fmt.Errorf("media: mark orphaned: %w", err)
	}
	defer rows.Close()
	return scanOrphans(rows)
}

// ListOrphaned returns assets an earlier sweep marked but did not finish.
func (r *Repo) ListOrphaned(ctx context.Context, limit int) ([]Orphan, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, coalesce(storage_key,''), coalesce(poster_key,''), coalesce(hls_key,'')
		  FROM media_assets WHERE status = 'orphaned'
		 ORDER BY created_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("media: list orphaned: %w", err)
	}
	defer rows.Close()
	return scanOrphans(rows)
}

func scanOrphans(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]Orphan, error) {
	out := []Orphan{}
	for rows.Next() {
		var o Orphan
		var storage, poster, hls string
		if err := rows.Scan(&o.ID, &storage, &poster, &hls); err != nil {
			return nil, fmt.Errorf("media: scan orphan: %w", err)
		}
		o.Keys = []string{storage, poster, hls}
		out = append(out, o)
	}
	return out, rows.Err()
}

// DeleteAsset removes the row once its objects are gone.
func (r *Repo) DeleteAsset(ctx context.Context, id uuid.UUID) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM media_assets WHERE id = $1`, id); err != nil {
		return fmt.Errorf("media: delete asset: %w", err)
	}
	return nil
}
