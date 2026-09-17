package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/objstore"
)

// Worker drains the transcode queue.
type Worker struct {
	id         string
	repo       *Repo
	store      objstore.Store
	queue      *Queue
	transcoder Transcoder
	logger     *slog.Logger

	// Batch is how many jobs to claim per poll; PollInterval is the idle wait.
	Batch        int
	PollInterval time.Duration
	// StaleAfter is how long a claimed job may sit before the sweeper frees it,
	// which is how a worker killed mid-job releases its work.
	StaleAfter time.Duration
}

func NewWorker(id string, repo *Repo, store objstore.Store, queue *Queue,
	transcoder Transcoder, logger *slog.Logger) *Worker {
	return &Worker{
		id: id, repo: repo, store: store, queue: queue,
		transcoder: transcoder, logger: logger,
		Batch: 2, PollInterval: 2 * time.Second, StaleAfter: 30 * time.Minute,
	}
}

// Run drains the queue until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	sweep := time.NewTicker(w.StaleAfter / 2)
	defer sweep.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sweep.C:
			if n, err := w.queue.SweepStale(ctx, w.StaleAfter); err != nil {
				w.logger.Error("sweep stale jobs", slog.Any("error", err))
			} else if n > 0 {
				w.logger.Warn("released stale jobs", slog.Int64("count", n))
			}
		default:
		}

		n, err := w.ProcessBatch(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			w.logger.Error("process batch", slog.Any("error", err))
		}
		if n == 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(w.PollInterval):
			}
		}
	}
}

// ProcessBatch claims and runs up to Batch jobs, returning how many ran.
func (w *Worker) ProcessBatch(ctx context.Context) (int, error) {
	jobs, err := w.queue.Claim(ctx, w.id, w.Batch)
	if err != nil {
		return 0, err
	}
	for _, job := range jobs {
		w.runJob(ctx, job)
	}
	return len(jobs), nil
}

func (w *Worker) runJob(ctx context.Context, job Job) {
	log := w.logger.With(slog.Int64("job_id", job.ID), slog.String("kind", job.Kind))

	if job.Kind != JobKindTranscode {
		// Nothing can handle it; failing it is better than silently completing.
		_ = w.queue.Fail(ctx, job.ID, fmt.Errorf("media: unknown job kind %q", job.Kind))
		return
	}
	var payload TranscodePayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		_ = w.queue.Fail(ctx, job.ID, fmt.Errorf("media: bad payload: %w", err))
		return
	}

	if err := w.Transcode(ctx, payload.AssetID); err != nil {
		log.Error("transcode failed",
			slog.String("asset_id", payload.AssetID.String()), slog.Any("error", err))
		_ = w.queue.Fail(ctx, job.ID, err)
		return
	}
	if err := w.queue.Complete(ctx, job.ID); err != nil {
		log.Error("complete job", slog.Any("error", err))
	}
}

// Transcode processes one asset end to end.
//
// It is idempotent by design: an asset already 'ready' returns immediately
// without re-transcoding, so a redelivered job cannot overwrite good
// derivatives or double-charge CPU. That matters because the queue guarantees
// at-least-once delivery, not exactly-once.
func (w *Worker) Transcode(ctx context.Context, assetID uuid.UUID) error {
	asset, err := w.repo.Get(ctx, assetID)
	if err != nil {
		return err
	}
	if asset.Status == StatusReady {
		w.logger.Info("asset already processed, skipping",
			slog.String("asset_id", assetID.String()))
		return nil
	}
	if asset.Status != StatusUploaded && asset.Status != StatusProcessing {
		return fmt.Errorf("media: asset %s is %s, not ready to transcode", assetID, asset.Status)
	}
	if err := w.repo.MarkProcessing(ctx, assetID); err != nil {
		return err
	}

	srcPath, cleanup, err := w.download(ctx, asset)
	if err != nil {
		_ = w.repo.MarkFailed(ctx, assetID, "не удалось прочитать исходный файл")
		return err
	}
	defer cleanup()

	var result TranscodeResult
	switch asset.Kind {
	case KindPhoto:
		result, err = w.transcoder.TranscodePhoto(ctx, srcPath, assetID)
	case KindVideo:
		result, err = w.transcoder.TranscodeVideo(ctx, srcPath, assetID)
	default:
		err = errUnknownKind(asset.Kind)
	}
	if err != nil {
		_ = w.repo.MarkFailed(ctx, assetID, "не удалось обработать файл")
		return err
	}

	for _, d := range result.Derivatives {
		if err := w.store.Put(ctx, d.Key, bytesReader(d.Data), int64(len(d.Data)), d.ContentType); err != nil {
			_ = w.repo.MarkFailed(ctx, assetID, "не удалось сохранить обработанный файл")
			return fmt.Errorf("media: upload derivative %s: %w", d.Key, err)
		}
	}
	return w.repo.MarkReady(ctx, assetID, result)
}

// download streams the original to a temp file, because ffmpeg needs a
// seekable path and holding a multi-gigabyte video in memory is not an option.
func (w *Worker) download(ctx context.Context, asset Asset) (string, func(), error) {
	dir, err := os.MkdirTemp("", "sk-media-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("media: temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	rc, err := w.store.Get(ctx, asset.StorageKey)
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("media: fetch original: %w", err)
	}
	defer rc.Close()

	path := filepath.Join(dir, "source"+filepath.Ext(asset.StorageKey))
	f, err := os.Create(path)
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("media: create temp file: %w", err)
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("media: write temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("media: close temp file: %w", err)
	}
	return path, cleanup, nil
}
