// Package objstore abstracts S3-compatible object storage behind a small
// interface, so the rest of the server neither imports an S3 SDK directly nor
// ever streams media bytes through the API process itself. Callers get
// presigned URLs for client-side uploads/downloads and short-lived, CDN-first
// public URLs for reads; only the media worker uses Put/Get directly, to move
// bytes between the store and ffmpeg during transcoding.
package objstore

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned by Stat, Get and Delete when the key does not exist
// in the bucket. Callers check it with errors.Is rather than matching a
// backend-specific error type.
var ErrNotFound = errors.New("objstore: object not found")

// ObjectInfo is the subset of S3 metadata callers need. It always reflects
// what the backend actually stored, never what a client claimed — Stat is the
// mechanism by which the media service verifies an upload before trusting it.
type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
}

// Store is an S3-compatible object storage backend. Implementations must be
// safe for concurrent use.
type Store interface {
	// PresignPut returns a short-lived URL the caller may issue a single PUT
	// request against to upload the object directly to the backend, without
	// the API process ever seeing the bytes. expiry should be minutes, not
	// hours: a leaked upload URL should stop working quickly.
	PresignPut(ctx context.Context, key string, expiry time.Duration) (string, error)

	// PresignGet returns a short-lived URL for reading the object directly.
	// It exists for completeness (e.g. an admin tool fetching an original);
	// ordinary reads go through PublicURL and the CDN instead.
	PresignGet(ctx context.Context, key string, expiry time.Duration) (string, error)

	// Put uploads data to key entirely server-side. size must be the exact
	// byte count of r's remaining content.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error

	// Get opens the object for reading. The caller must Close it.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Stat returns the object's real, backend-recorded metadata, or
	// ErrNotFound if it does not exist. This is the only trustworthy source
	// of an uploaded object's size and content type — a client's declared
	// values are never sufficient on their own.
	Stat(ctx context.Context, key string) (ObjectInfo, error)

	// Delete removes the object. Deleting a missing key is not an error.
	Delete(ctx context.Context, key string) error

	// PublicURL builds the CDN-facing URL for a key. The API must never
	// stream original or derivative media itself; every response that
	// references media hands the client one of these URLs instead.
	PublicURL(key string) string
}

// publicURL builds a public URL for key, preferring the CDN base when one is
// configured and falling back to a path-style URL against the storage
// endpoint otherwise. It is a free function, independent of any client, so it
// can be unit tested without a live backend.
func publicURL(cdnBase, endpoint, bucket string, useSSL bool, key string) string {
	key = trimSlashes(key)
	if cdnBase != "" {
		return trimSlashes(cdnBase) + "/" + key
	}
	scheme := "http"
	if useSSL {
		scheme = "https"
	}
	return scheme + "://" + trimSlashes(endpoint) + "/" + trimSlashes(bucket) + "/" + key
}

func trimSlashes(s string) string {
	start := 0
	end := len(s)
	for start < end && s[start] == '/' {
		start++
	}
	for end > start && s[end-1] == '/' {
		end--
	}
	return s[start:end]
}
