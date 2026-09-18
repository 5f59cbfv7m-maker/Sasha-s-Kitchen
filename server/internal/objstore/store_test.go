package objstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestPublicURL_PrefersCDN(t *testing.T) {
	got := publicURL("https://cdn.example.com/", "minio.internal:9000", "sk-media", true, "/media/originals/abc.jpg")
	want := "https://cdn.example.com/media/originals/abc.jpg"
	if got != want {
		t.Fatalf("publicURL = %q, want %q", got, want)
	}
}

func TestPublicURL_FallsBackToEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		useSSL   bool
		endpoint string
		bucket   string
		key      string
		want     string
	}{
		{"ssl", true, "minio.internal:9000", "sk-media", "media/a.jpg", "https://minio.internal:9000/sk-media/media/a.jpg"},
		{"no ssl", false, "127.0.0.1:9000", "sk-media", "media/a.jpg", "http://127.0.0.1:9000/sk-media/media/a.jpg"},
		{"slashes trimmed", true, "/minio.internal:9000/", "/sk-media/", "//media/a.jpg/", "https://minio.internal:9000/sk-media/media/a.jpg"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := publicURL("", c.endpoint, c.bucket, c.useSSL, c.key)
			if got != c.want {
				t.Fatalf("publicURL = %q, want %q", got, c.want)
			}
		})
	}
}

func TestFake_PutGetStatDelete(t *testing.T) {
	ctx := context.Background()
	f := NewFake()

	if _, err := f.Stat(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat on missing key: got %v, want ErrNotFound", err)
	}
	if _, err := f.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on missing key: got %v, want ErrNotFound", err)
	}

	data := []byte("hello world")
	if err := f.Put(ctx, "k1", bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	info, err := f.Stat(ctx, "k1")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size != int64(len(data)) || info.ContentType != "text/plain" {
		t.Fatalf("Stat = %+v, want size=%d contentType=text/plain", info, len(data))
	}

	rc, err := f.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("Get content = %q, want %q", got, data)
	}

	if err := f.Delete(ctx, "k1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := f.Stat(ctx, "k1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat after delete: got %v, want ErrNotFound", err)
	}
}

func TestFake_Seed(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	f.Seed("uploaded/key.jpg", []byte("bytes"), "image/jpeg")

	info, err := f.Stat(ctx, "uploaded/key.jpg")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.ContentType != "image/jpeg" || info.Size != 5 {
		t.Fatalf("Stat = %+v, want contentType=image/jpeg size=5", info)
	}
}

func TestFake_PutCount(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	data := []byte("x")
	for range 3 {
		if err := f.Put(ctx, "derivative", bytes.NewReader(data), 1, "image/jpeg"); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if got := f.PutCount("derivative"); got != 3 {
		t.Fatalf("PutCount = %d, want 3", got)
	}
	if got := f.PutCount("other"); got != 0 {
		t.Fatalf("PutCount(other) = %d, want 0", got)
	}
}

func TestFake_PresignURLsCarryExpiry(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	putURL, err := f.PresignPut(ctx, "k", 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignPut: %v", err)
	}
	getURL, err := f.PresignGet(ctx, "k", 5*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	if putURL == "" || getURL == "" || putURL == getURL {
		t.Fatalf("expected distinct non-empty presigned URLs, got put=%q get=%q", putURL, getURL)
	}
}

func TestFake_PublicURL(t *testing.T) {
	f := NewFake()
	if got := f.PublicURL("/media/a.jpg/"); got != "https://fake-cdn.local/media/a.jpg" {
		t.Fatalf("PublicURL = %q", got)
	}
}
