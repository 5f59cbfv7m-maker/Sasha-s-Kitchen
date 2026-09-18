package objstore

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/config"
)

// S3Store is a Store backed by any S3-compatible service (MinIO self-hosted,
// or a cloud S3-compatible provider) via minio-go.
type S3Store struct {
	client   *minio.Client
	bucket   string
	cdnBase  string
	endpoint string
	useSSL   bool
}

// New builds an S3Store from the application's storage configuration. It does
// not verify the bucket exists; call EnsureBucket at boot if that matters.
func New(cfg config.StorageConfig) (*S3Store, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("objstore: create minio client: %w", err)
	}
	return &S3Store{
		client:   client,
		bucket:   cfg.Bucket,
		cdnBase:  cfg.PublicCDN,
		endpoint: cfg.Endpoint,
		useSSL:   cfg.UseSSL,
	}, nil
}

// EnsureBucket creates the configured bucket if it does not already exist.
// Safe to call on every boot.
func (s *S3Store) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("objstore: check bucket: %w", err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("objstore: make bucket: %w", err)
	}
	return nil
}

func (s *S3Store) PresignPut(ctx context.Context, key string, expiry time.Duration) (string, error) {
	u, err := s.client.PresignedPutObject(ctx, s.bucket, key, expiry)
	if err != nil {
		return "", fmt.Errorf("objstore: presign put %q: %w", key, err)
	}
	return u.String(), nil
}

func (s *S3Store) PresignGet(ctx context.Context, key string, expiry time.Duration) (string, error) {
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, expiry, url.Values{})
	if err != nil {
		return "", fmt.Errorf("objstore: presign get %q: %w", key, err)
	}
	return u.String(), nil
}

func (s *S3Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("objstore: put %q: %w", key, err)
	}
	return nil
}

func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("objstore: get %q: %w", key, err)
	}
	// GetObject itself rarely errors on a missing key (it is lazy); force a
	// Stat so a missing object is reported as ErrNotFound up front rather than
	// surfacing as an opaque read error to whatever consumes the reader.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("objstore: stat on get %q: %w", key, err)
	}
	return obj, nil
}

func (s *S3Store) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return ObjectInfo{}, ErrNotFound
		}
		return ObjectInfo{}, fmt.Errorf("objstore: stat %q: %w", key, err)
	}
	return ObjectInfo{
		Key:          key,
		Size:         info.Size,
		ContentType:  info.ContentType,
		ETag:         info.ETag,
		LastModified: info.LastModified,
	}, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("objstore: delete %q: %w", key, err)
	}
	return nil
}

func (s *S3Store) PublicURL(key string) string {
	return publicURL(s.cdnBase, s.endpoint, s.bucket, s.useSSL, key)
}

func isNotFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == minio.NoSuchKey || resp.Code == "NotFound" || resp.StatusCode == 404
}
