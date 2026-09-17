package media

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// Derivative is one output file a Transcoder produced, ready to be uploaded
// to the object store at Key.
type Derivative struct {
	Key         string
	ContentType string
	Size        int64
	Data        []byte
}

// TranscodeResult is everything a Transcoder learns about the source file
// plus the derivatives it produced. Width/Height/DurationMs/PosterKey/HLSKey
// map directly onto the matching media_assets columns; DurationMs is left 0
// for photos.
type TranscodeResult struct {
	Width, Height int
	DurationMs    int
	PosterKey     string // video: true poster frame. photo: unused (see Blurhash/Derivatives).
	HLSKey        string // video only: master manifest key.
	Blurhash      string
	Derivatives   []Derivative
}

// Transcoder turns an original media file, already downloaded to a local
// path, into the derivatives the storefront actually serves. Implementations
// must not mutate or delete srcPath.
type Transcoder interface {
	TranscodePhoto(ctx context.Context, srcPath string, assetID uuid.UUID) (TranscodeResult, error)
	TranscodeVideo(ctx context.Context, srcPath string, assetID uuid.UUID) (TranscodeResult, error)
}

// FakeTranscoder is a Transcoder double for tests. Set PhotoResult/VideoResult
// to control what a call returns, or Err to make every call fail. Calls are
// counted so tests can assert how many times transcoding actually ran (e.g.
// to prove a re-run of an already-ready job did not transcode again).
type FakeTranscoder struct {
	mu sync.Mutex

	PhotoResult TranscodeResult
	VideoResult TranscodeResult
	Err         error

	PhotoCalls int
	VideoCalls int
}

func (f *FakeTranscoder) TranscodePhoto(_ context.Context, _ string, _ uuid.UUID) (TranscodeResult, error) {
	f.mu.Lock()
	f.PhotoCalls++
	f.mu.Unlock()
	if f.Err != nil {
		return TranscodeResult{}, f.Err
	}
	return f.PhotoResult, nil
}

func (f *FakeTranscoder) TranscodeVideo(_ context.Context, _ string, _ uuid.UUID) (TranscodeResult, error) {
	f.mu.Lock()
	f.VideoCalls++
	f.mu.Unlock()
	if f.Err != nil {
		return TranscodeResult{}, f.Err
	}
	return f.VideoResult, nil
}

var _ Transcoder = (*FakeTranscoder)(nil)

// errUnknownKind is returned by the worker when an asset's kind is neither
// "photo" nor "video" — should be unreachable given the CHECK constraint and
// the service's own validation, but handled explicitly rather than assumed.
func errUnknownKind(kind Kind) error {
	return fmt.Errorf("media: unknown asset kind %q", kind)
}
