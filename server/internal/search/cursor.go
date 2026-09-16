package search

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// cursorVersion guards the encoding. Bumping it invalidates old cursors rather
// than letting a changed shape be misread as valid.
const cursorVersion = 1

// Cursor is an opaque keyset position. It carries the sort it was produced
// under so a client cannot page through one ordering using another's cursor,
// which would silently skip or duplicate rows.
//
// Keyset beats OFFSET here for two reasons: cost stays flat at any depth, and
// rows inserted while the user pages cannot shift the window under them.
type Cursor struct {
	Version int       `json:"v"`
	Sort    SortMode  `json:"s"`
	ID      string    `json:"i"`
	Num     float64   `json:"n,omitempty"`
	Time    time.Time `json:"t,omitempty"`
}

// Encode renders the cursor for transport.
func (c Cursor) Encode() string {
	c.Version = cursorVersion
	raw, err := json.Marshal(c)
	if err != nil {
		// The struct is fixed and always marshalable; an error here is a bug,
		// and an empty cursor degrades to "first page" rather than breaking.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor parses and validates a cursor produced by Encode.
func DecodeCursor(s string) (*Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("cursor: decode: %w", err)
	}
	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("cursor: unmarshal: %w", err)
	}
	if c.Version != cursorVersion {
		return nil, errors.New("cursor: unsupported version")
	}
	if c.ID == "" {
		return nil, errors.New("cursor: missing id")
	}
	switch c.Sort {
	case SortRelevance, SortNew, SortPopular, SortRating, SortQuick, SortLight:
	default:
		return nil, errors.New("cursor: unknown sort")
	}
	return &c, nil
}
