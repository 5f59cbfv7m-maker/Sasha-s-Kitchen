// Package paging carries the keyset cursor shared by every listing in the
// store: recipes, an author's shelf, the blog, the shorts feed, comment
// threads and the leaderboard.
//
// It used to live in internal/search, where DecodeCursor validated the sort
// against the six recipe orderings. Every new listing would then have had to
// either widen that switch -- coupling the whole store to the recipe search
// package -- or invent a second, incompatible cursor. Neither is worth it, so
// the sort is a free-form string here and each listing validates its own.
package paging

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Version guards the encoding. Bumping it invalidates old cursors rather than
// letting a changed shape be misread as valid.
const Version = 2

// Cursor is an opaque keyset position.
//
// Keyset beats OFFSET for two reasons: cost stays flat at any depth, and rows
// inserted while the user pages cannot shift the window under them.
//
// It carries the sort it was produced under, so a client cannot page one
// ordering with another's cursor and silently skip or duplicate rows.
type Cursor struct {
	Version int    `json:"v"`
	Sort    string `json:"s"`
	ID      string `json:"i"`
	// Num and Time hold whichever sort key the ordering uses; exactly one is
	// meaningful for any given sort.
	Num  float64   `json:"n,omitempty"`
	Time time.Time `json:"t,omitempty"`
	// Epoch pins the cursor to a generation of a recomputed ranking.
	//
	// A ranked feed orders by a score a background job rewrites. Paging across
	// a rewrite with a stale cursor skips and repeats rows, which on a vertical
	// video feed is exactly the failure users notice. Listings that rank this
	// way compare Epoch and start the reader over rather than lie to them.
	// Chronological listings leave it zero.
	Epoch int64 `json:"e,omitempty"`
}

// Encode renders the cursor for transport.
func (c Cursor) Encode() string {
	c.Version = Version
	raw, err := json.Marshal(c)
	if err != nil {
		// The struct is fixed and always marshalable; an error here is a bug,
		// and an empty cursor degrades to "first page" rather than breaking.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Decode parses a cursor produced by Encode and checks everything that does
// not depend on the listing. The caller must still check that Sort is one of
// its own orderings; DecodeFor does both.
func Decode(s string) (*Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("cursor: decode: %w", err)
	}
	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("cursor: unmarshal: %w", err)
	}
	if c.Version != Version {
		return nil, errors.New("cursor: unsupported version")
	}
	if c.ID == "" {
		return nil, errors.New("cursor: missing id")
	}
	if c.Sort == "" {
		return nil, errors.New("cursor: missing sort")
	}
	return &c, nil
}

// DecodeFor parses a cursor and rejects one produced under a sort this listing
// does not know.
func DecodeFor(s string, allowed ...string) (*Cursor, error) {
	c, err := Decode(s)
	if err != nil {
		return nil, err
	}
	for _, a := range allowed {
		if c.Sort == a {
			return c, nil
		}
	}
	return nil, errors.New("cursor: unknown sort")
}
