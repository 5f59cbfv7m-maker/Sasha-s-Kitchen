package media

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/jobs"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/objstore"
)

// JobKindTranscode is the jobs.kind value this package's handler answers to.
const JobKindTranscode = "media.transcode"

// Worker turns media assets into their derivatives. Claiming, retrying and
// scheduling belong to internal/jobs; this type only knows how to transcode.
type Worker struct {
	repo       *Repo
	store      objstore.Store
	transcoder Transcoder
	logger     *slog.Logger
}

func NewWorker(repo *Repo, store objstore.Store,
	transcoder Transcoder, logger *slog.Logger) *Worker {
	return &Worker{repo: repo, store: store, transcoder: transcoder, logger: logger}
}

// Handler adapts this worker to the job runner.
func (w *Worker) Handler() jobs.Handler {
	return func(ctx context.Context, job jobs.Job) error {
		var payload TranscodePayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("media: bad payload: %w", err)
		}
		return w.Transcode(ctx, payload.AssetID)
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
