package media

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Kind mirrors media_assets.kind's CHECK constraint exactly.
type Kind string

const (
	KindPhoto Kind = "photo"
	KindVideo Kind = "video"
)

// Status mirrors media_assets.status's CHECK constraint exactly.
type Status string

const (
	StatusPending    Status = "pending"
	StatusUploaded   Status = "uploaded"
	StatusProcessing Status = "processing"
	StatusReady      Status = "ready"
	StatusFailed     Status = "failed"
)

// Asset mirrors one row of media_assets.
//
// PosterKey and HLSKey are video-shaped names but are reused for photos too:
// the migrated schema has no photo-specific derivative columns, and adding
// some was judged not worth a migration for this feature. For a photo,
// PosterKey holds the "card"-sized derivative (see PhotoDerivativeKey) — the
// one representative image callers show by default — and HLSKey stays nil.
// The thumb and full sizes are never written to the database at all: their
// keys are deterministic functions of the asset id (PhotoDerivativeKey), so
// both the worker (writing them) and the API (building their URLs) derive
// the same key independently instead of storing three more columns.
type Asset struct {
	ID          uuid.UUID
	OwnerID     uuid.UUID
	Kind        Kind
	Status      Status
	StorageKey  string
	ContentType string
	Bytes       int64
	Width       *int
	Height      *int
	DurationMs  *int
	PosterKey   *string
	HLSKey      *string
	Blurhash    *string
	Error       *string
	CreatedAt   time.Time
	ProcessedAt *time.Time
}

// photoContentTypes maps an allowed client-declared (and server-verified)
// photo content type to the file extension used in storage keys.
var photoContentTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/heic": ".heic",
	"image/webp": ".webp",
}

// videoContentTypes maps an allowed video content type to its extension.
var videoContentTypes = map[string]string{
	"video/mp4":       ".mp4",
	"video/quicktime": ".mov",
}

const (
	maxPhotoBytes int64 = 25 << 20 // 25 MiB
	maxVideoBytes int64 = 2 << 30  // 2 GiB
)

// allowedContentTypes returns the allow-list map for kind, or nil if kind is
// not a recognised value.
func allowedContentTypes(kind Kind) map[string]string {
	switch kind {
	case KindPhoto:
		return photoContentTypes
	case KindVideo:
		return videoContentTypes
	default:
		return nil
	}
}

// maxBytesFor returns the upload size cap for kind, or 0 if kind is not
// recognised.
func maxBytesFor(kind Kind) int64 {
	switch kind {
	case KindPhoto:
		return maxPhotoBytes
	case KindVideo:
		return maxVideoBytes
	default:
		return 0
	}
}

// objectKey builds the storage key for an original upload. It is derived
// only from the asset's own UUID and the (validated) content type's
// extension — never from a client-supplied filename, so it cannot be used to
// traverse the bucket or collide with another object.
func objectKey(id uuid.UUID, ext string) string {
	return fmt.Sprintf("media/originals/%s%s", id, ext)
}

// PhotoDerivativeKey returns the deterministic storage key for one of a
// photo's resized derivatives. size must be "thumb", "card" or "full".
func PhotoDerivativeKey(id uuid.UUID, size string) string {
	return fmt.Sprintf("media/derivatives/%s/%s.jpg", id, size)
}

// VideoPosterKey returns the deterministic storage key for a video's poster
// frame.
func VideoPosterKey(id uuid.UUID) string {
	return fmt.Sprintf("media/derivatives/%s/poster.jpg", id)
}

// VideoHLSKey returns the deterministic storage key for a video's master HLS
// manifest.
func VideoHLSKey(id uuid.UUID) string {
	return fmt.Sprintf("media/derivatives/%s/hls/master.m3u8", id)
}

// VideoRungKey returns the deterministic storage key for one rung's playlist
// within a video's HLS ladder. rung is e.g. "480p", "720p", "1080p".
func VideoRungKey(id uuid.UUID, rung string) string {
	return fmt.Sprintf("media/derivatives/%s/hls/%s/playlist.m3u8", id, rung)
}

// VideoSegmentKey returns the deterministic storage key for one media
// segment within a rung.
func VideoSegmentKey(id uuid.UUID, rung string, segment int) string {
	return fmt.Sprintf("media/derivatives/%s/hls/%s/seg%04d.ts", id, rung, segment)
}
