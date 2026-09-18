package author

import (
	"encoding/json"
	"net/url"
	"strings"
)

// maxLinks mirrors the CHECK on users.links in migration 0007.
const maxLinks = 8

const (
	maxLinkTitle = 40
	maxLinkURL   = 300
)

// decodeLinks reads the stored links array defensively.
//
// The column is jsonb with only a shape constraint, so anything that ever
// wrote to it badly must not be able to break a profile render. Entries that
// do not survive validation are dropped rather than rendered.
func decodeLinks(raw []byte) []Link {
	var stored []Link
	if err := json.Unmarshal(raw, &stored); err != nil {
		return []Link{}
	}
	out := make([]Link, 0, len(stored))
	for _, l := range stored {
		if l, ok := SanitizeLink(l); ok {
			out = append(out, l)
		}
		if len(out) == maxLinks {
			break
		}
	}
	return out
}

// SanitizeLink trims a link and reports whether it is safe to store and show.
//
// Only http and https are allowed: a javascript: or data: URL rendered as a
// profile link is a cross-site scripting delivery mechanism on whichever
// client eventually opens it.
func SanitizeLink(l Link) (Link, bool) {
	l.Title = strings.TrimSpace(l.Title)
	l.URL = strings.TrimSpace(l.URL)
	if l.URL == "" || len(l.URL) > maxLinkURL || len([]rune(l.Title)) > maxLinkTitle {
		return Link{}, false
	}
	u, err := url.Parse(l.URL)
	if err != nil || u.Host == "" {
		return Link{}, false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return Link{}, false
	}
	if l.Title == "" {
		l.Title = u.Host
	}
	return l, true
}
