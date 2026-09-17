package media

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrAssetNotFound is returned by Repo.Get when no row matches the id.
var ErrAssetNotFound = errors.New("media: asset not found")

const assetColumns = `id, owner_id, kind, status, storage_key, content_type, bytes,
	width, height, duration_ms, poster_key, hls_key, blurhash, error,
	created_at, processed_at`

// Repo is the Postgres-backed store for media_assets. Every mutating method
// carries a status guard in its WHERE clause: that is what makes the worker's
// repeated calls (on a redelivered job, or a client retrying /complete)
// idempotent instead of corrupting a row that another call already advanced.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo builds a Repo over an existing pool.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// Create inserts a new pending asset row. a.ID, a.Status and a.CreatedAt are
// set by the database and written back into a.
func (r *Repo) Create(ctx context.Context, a *Asset) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	a.Status = StatusPending
	row := r.pool.QueryRow(ctx, `
		INSERT INTO media_assets (id, owner_id, kind, status, storage_key, content_type)
		VALUES ($1, $2, $3, 'pending', $4, $5)
		RETURNING created_at`,
		a.ID, a.OwnerID, string(a.Kind), a.StorageKey, nullableString(a.ContentType))
	if err := row.Scan(&a.CreatedAt); err != nil {
		return fmt.Errorf("media: insert asset: %w", err)
	}
	return nil
}

// Get loads one asset by id.
func (r *Repo) Get(ctx context.Context, id uuid.UUID) (Asset, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+assetColumns+` FROM media_assets WHERE id = $1`, id)
	a, err := scanAsset(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrAssetNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("media: get asset %s: %w", id, err)
	}
	return a, nil
}

// MarkUploaded records the real, server-verified content type and size and
// moves the asset from pending to uploaded. Guarded to pending so calling it
// twice (a client retrying POST /media/{id}/complete) is a harmless no-op the
// second time; the caller can tell which happened by comparing the returned
// bool.
func (r *Repo) MarkUploaded(ctx context.Context, id uuid.UUID, contentType string, bytes int64) (bool, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE media_assets
		SET status = 'uploaded', content_type = $2, bytes = $3
		WHERE id = $1 AND status = 'pending'`,
		id, contentType, bytes)
	if err != nil {
		return false, fmt.Errorf("media: mark uploaded %s: %w", id, err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkProcessing moves the asset into processing. Guarded to uploaded or
// already-processing so a redelivered job re-entering this state is a no-op
// rather than an error.
func (r *Repo) MarkProcessing(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE media_assets SET status = 'processing'
		WHERE id = $1 AND status IN ('uploaded', 'processing')`, id)
	if err != nil {
		return fmt.Errorf("media: mark processing %s: %w", id, err)
	}
	return nil
}

// MarkReady records transcode output and moves the asset to ready, clearing
// any previous error. Not guarded by current status: a redelivered job whose
// derivatives already exist at the same deterministic keys re-applies the
// same values harmlessly (see Worker.handleTranscode for the higher-level
// idempotency check that avoids redoing the work at all).
func (r *Repo) MarkReady(ctx context.Context, id uuid.UUID, res TranscodeResult) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE media_assets SET
			status = 'ready',
			width = $2,
			height = $3,
			duration_ms = $4,
			poster_key = $5,
			hls_key = $6,
			blurhash = $7,
			error = NULL,
			processed_at = now()
		WHERE id = $1`,
		id,
		nullableInt(res.Width),
		nullableInt(res.Height),
		nullableInt(res.DurationMs),
		nullableString(res.PosterKey),
		nullableString(res.HLSKey),
		nullableString(res.Blurhash))
	if err != nil {
		return fmt.Errorf("media: mark ready %s: %w", id, err)
	}
	return nil
}

// MarkFailed records a failure. msg is stored verbatim and is safe to show to
// the uploading user, so it must already be in Russian.
func (r *Repo) MarkFailed(ctx context.Context, id uuid.UUID, msg string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE media_assets SET status = 'failed', error = $2, processed_at = now()
		WHERE id = $1`, id, msg)
	if err != nil {
		return fmt.Errorf("media: mark failed %s: %w", id, err)
	}
	return nil
}

// row is the minimal subset of pgx.Row that scanAsset needs, so it works
// against both QueryRow and a Rows cursor.
type row interface {
	Scan(dest ...any) error
}

func scanAsset(r row) (Asset, error) {
	var a Asset
	var kind, status string
	var contentType *string
	// bytes is NULL until the upload is verified against the bucket, so it is
	// scanned through a pointer and flattened to 0. Scanning straight into
	// Asset.Bytes fails on every pending asset.
	var size *int64
	err := r.Scan(
		&a.ID, &a.OwnerID, &kind, &status, &a.StorageKey, &contentType, &size,
		&a.Width, &a.Height, &a.DurationMs, &a.PosterKey, &a.HLSKey, &a.Blurhash, &a.Error,
		&a.CreatedAt, &a.ProcessedAt,
	)
	if err != nil {
		return Asset{}, err
	}
	a.Kind = Kind(kind)
	a.Status = Status(status)
	if contentType != nil {
		a.ContentType = *contentType
	}
	if size != nil {
		a.Bytes = *size
	}
	return a, nil
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullableInt(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}
