package media

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/jobs"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/objstore"
)

// presignTTL bounds how long an upload ticket stays usable. Short on purpose:
// a leaked URL is a write grant into the bucket.
const presignTTL = 15 * time.Minute

var (
	// ErrUnsupportedType is returned for a content type outside the allow-list.
	ErrUnsupportedType = errors.New("media: unsupported content type")
	// ErrTooLarge is returned when the declared or actual size exceeds the cap.
	ErrTooLarge = errors.New("media: file too large")
	// ErrNotUploaded is returned when the object is not actually in the bucket.
	ErrNotUploaded = errors.New("media: object not found in storage")
	// ErrForbidden is returned when the caller does not own the asset.
	ErrForbidden = errors.New("media: not the owner")
)

// Service owns the upload lifecycle.
type Service struct {
	repo  *Repo
	store objstore.Store
	queue *jobs.Queue
}

func NewService(repo *Repo, store objstore.Store, queue *jobs.Queue) *Service {
	return &Service{repo: repo, store: store, queue: queue}
}

// Ticket is what a client needs to upload directly to object storage. The API
// never proxies the bytes: originals go straight to the bucket.
type Ticket struct {
	AssetID   uuid.UUID `json:"asset_id"`
	UploadURL string    `json:"upload_url"`
	Method    string    `json:"method"`
	ExpiresIn int       `json:"expires_in_seconds"`
	MaxBytes  int64     `json:"max_bytes"`
}

// CreateUploadTicket validates the declared type and size, reserves an asset
// row, and returns a presigned PUT URL.
//
// declaredBytes is advisory only — it lets an oversized upload be refused
// before it is transferred. The authoritative size check happens in
// CompleteUpload against what is actually in the bucket.
func (s *Service) CreateUploadTicket(ctx context.Context, ownerID uuid.UUID,
	kind Kind, contentType string, declaredBytes int64) (Ticket, error) {

	allowed := allowedContentTypes(kind)
	if allowed == nil {
		return Ticket{}, fmt.Errorf("%w: unknown kind %q", ErrUnsupportedType, kind)
	}
	ext, ok := allowed[contentType]
	if !ok {
		return Ticket{}, fmt.Errorf("%w: %s", ErrUnsupportedType, contentType)
	}
	maxBytes := maxBytesFor(kind)
	if declaredBytes > maxBytes {
		return Ticket{}, fmt.Errorf("%w: %d > %d", ErrTooLarge, declaredBytes, maxBytes)
	}

	asset := &Asset{
		ID:          uuid.New(),
		OwnerID:     ownerID,
		Kind:        kind,
		Status:      StatusPending,
		ContentType: contentType,
	}
	asset.StorageKey = objectKey(asset.ID, ext)

	if err := s.repo.Create(ctx, asset); err != nil {
		return Ticket{}, err
	}
	url, err := s.store.PresignPut(ctx, asset.StorageKey, presignTTL)
	if err != nil {
		return Ticket{}, fmt.Errorf("media: presign upload: %w", err)
	}
	return Ticket{
		AssetID:   asset.ID,
		UploadURL: url,
		Method:    "PUT",
		ExpiresIn: int(presignTTL.Seconds()),
		MaxBytes:  maxBytes,
	}, nil
}

// CompleteUpload verifies the object really landed in the bucket, records its
// true size and type, and queues transcoding.
//
// Nothing the client says about the file is trusted here: a client can declare
// a 1 KB JPEG and upload a 2 GB file, so the size and type that get recorded
// come from Stat against the bucket, not from the request.
func (s *Service) CompleteUpload(ctx context.Context, assetID, callerID uuid.UUID) (Asset, error) {
	asset, err := s.repo.Get(ctx, assetID)
	if err != nil {
		return Asset{}, err
	}
	if asset.OwnerID != callerID {
		return Asset{}, ErrForbidden
	}
	// Already past this point: return as-is so a retried request is harmless.
	if asset.Status != StatusPending {
		return asset, nil
	}

	info, err := s.store.Stat(ctx, asset.StorageKey)
	if err != nil {
		return Asset{}, fmt.Errorf("%w: %s", ErrNotUploaded, asset.StorageKey)
	}
	if max := maxBytesFor(asset.Kind); info.Size > max {
		// The client lied about the size. Remove the object so a rejected
		// upload cannot squat in the bucket.
		_ = s.store.Delete(ctx, asset.StorageKey)
		_ = s.repo.MarkFailed(ctx, asset.ID, "файл превышает допустимый размер")
		return Asset{}, fmt.Errorf("%w: %d > %d", ErrTooLarge, info.Size, max)
	}
	// The stored type must still be one we accept; a presigned PUT does not
	// constrain what the client actually sent.
	contentType := info.ContentType
	if contentType == "" {
		contentType = asset.ContentType
	}
	if allowed := allowedContentTypes(asset.Kind); allowed != nil {
		if _, ok := allowed[contentType]; !ok {
			_ = s.store.Delete(ctx, asset.StorageKey)
			_ = s.repo.MarkFailed(ctx, asset.ID, "неподдерживаемый тип файла")
			return Asset{}, fmt.Errorf("%w: %s", ErrUnsupportedType, contentType)
		}
	}

	changed, err := s.repo.MarkUploaded(ctx, asset.ID, contentType, info.Size)
	if err != nil {
		return Asset{}, err
	}
	if changed {
		// Dedupe key makes a double completion enqueue one job, not two.
		if _, err := s.queue.Enqueue(ctx, JobKindTranscode,
			TranscodePayload{AssetID: asset.ID},
			jobs.WithDedupeKey("transcode:"+asset.ID.String())); err != nil {
			return Asset{}, fmt.Errorf("media: enqueue transcode: %w", err)
		}
	}
	return s.repo.Get(ctx, asset.ID)
}

// TranscodePayload is the job body for JobKindTranscode.
type TranscodePayload struct {
	AssetID uuid.UUID `json:"asset_id"`
}
