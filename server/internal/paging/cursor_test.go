package paging

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	want := Cursor{Sort: "new", ID: "11111111-1111-1111-1111-111111111111", Time: now, Epoch: 42}

	got, err := Decode(want.Encode())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Sort != want.Sort || got.ID != want.ID || got.Epoch != want.Epoch || !got.Time.Equal(now) {
		t.Errorf("round trip lost data: %+v != %+v", *got, want)
	}
	if got.Version != Version {
		t.Errorf("version = %d, want %d", got.Version, Version)
	}
}

// TestRejectsForeignSort is the guard that matters: continuing one ordering
// with another's cursor silently skips or repeats rows, and the reader has no
// way to notice.
func TestRejectsForeignSort(t *testing.T) {
	c := Cursor{Sort: "shorts_ranked", ID: "abc"}
	if _, err := DecodeFor(c.Encode(), "new", "popular"); err == nil {
		t.Error("a cursor from another listing was accepted")
	}
	if _, err := DecodeFor(c.Encode(), "new", "shorts_ranked"); err != nil {
		t.Errorf("a cursor from a known sort was rejected: %v", err)
	}
}

func TestRejectsMalformed(t *testing.T) {
	// An old v1 cursor, from when the sort was validated inside internal/search.
	v1, _ := json.Marshal(map[string]any{"v": 1, "s": "new", "i": "abc"})

	for name, raw := range map[string]string{
		"not base64":   "!!!!",
		"not json":     base64.RawURLEncoding.EncodeToString([]byte("nonsense")),
		"old version":  base64.RawURLEncoding.EncodeToString(v1),
		"missing id":   Cursor{Sort: "new"}.Encode(),
		"missing sort": Cursor{ID: "abc"}.Encode(),
		"empty":        "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(raw); err == nil {
				t.Errorf("Decode(%q) accepted a malformed cursor", raw)
			}
		})
	}
}

// TestEncodeIsURLSafe: cursors travel in query strings, so they must survive
// one without escaping.
func TestEncodeIsURLSafe(t *testing.T) {
	enc := Cursor{Sort: "new", ID: "11111111-1111-1111-1111-111111111111", Time: time.Now()}.Encode()
	for _, c := range enc {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			t.Fatalf("cursor contains %q, which needs escaping in a URL: %s", c, enc)
		}
	}
}
