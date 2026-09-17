package objstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// Fake is an in-memory Store for tests. It never touches the network; a
// PresignPut/PresignGet URL is a synthetic string, not something a real HTTP
// client can PUT to. Tests that need to simulate "the client already
// uploaded" call Seed directly instead of performing a real HTTP round trip.
type Fake struct {
	mu      sync.Mutex
	objects map[string]*fakeObject
	putN    map[string]int
	now     func() time.Time
}

type fakeObject struct {
	data        []byte
	contentType string
	modTime     time.Time
}

// NewFake returns an empty Fake store.
func NewFake() *Fake {
	return &Fake{
		objects: make(map[string]*fakeObject),
		putN:    make(map[string]int),
		now:     time.Now,
	}
}

// Seed injects an object as if a client had already uploaded it via a
// presigned PUT URL. Test-only; not part of the Store interface.
func (f *Fake) Seed(key string, data []byte, contentType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	f.objects[key] = &fakeObject{data: cp, contentType: contentType, modTime: f.now()}
}

// PutCount returns how many times Put has written to key. Tests use this to
// assert idempotent re-processing does not re-upload derivatives.
func (f *Fake) PutCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.putN[key]
}

// Keys returns every key currently stored, for assertions that don't want to
// guess an exact derivative key.
func (f *Fake) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.objects))
	for k := range f.objects {
		out = append(out, k)
	}
	return out
}

func (f *Fake) PresignPut(_ context.Context, key string, expiry time.Duration) (string, error) {
	exp := f.now().Add(expiry).Unix()
	return fmt.Sprintf("fake://put/%s?exp=%d", key, exp), nil
}

func (f *Fake) PresignGet(_ context.Context, key string, expiry time.Duration) (string, error) {
	exp := f.now().Add(expiry).Unix()
	return fmt.Sprintf("fake://get/%s?exp=%d", key, exp), nil
}

func (f *Fake) Put(_ context.Context, key string, r io.Reader, size int64, contentType string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("objstore/fake: read: %w", err)
	}
	if size >= 0 && int64(len(data)) != size {
		return fmt.Errorf("objstore/fake: declared size %d does not match %d bytes read", size, len(data))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = &fakeObject{data: data, contentType: contentType, modTime: f.now()}
	f.putN[key]++
	return nil
}

func (f *Fake) Get(_ context.Context, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	obj, ok := f.objects[key]
	f.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(obj.data)), nil
}

func (f *Fake) Stat(_ context.Context, key string) (ObjectInfo, error) {
	f.mu.Lock()
	obj, ok := f.objects[key]
	f.mu.Unlock()
	if !ok {
		return ObjectInfo{}, ErrNotFound
	}
	return ObjectInfo{
		Key:          key,
		Size:         int64(len(obj.data)),
		ContentType:  obj.contentType,
		LastModified: obj.modTime,
	}, nil
}

func (f *Fake) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.objects, key)
	return nil
}

func (f *Fake) PublicURL(key string) string {
	return "https://fake-cdn.local/" + trimSlashes(key)
}

var _ Store = (*Fake)(nil)
