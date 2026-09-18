package search

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/paging"
)

// MediaURLResolver turns a storage key into a URL a client can fetch, normally
// pointing at the CDN. Declared here as a one-method interface so the search
// package does not depend on the media or storage packages.
type MediaURLResolver interface {
	PublicURL(key string) string
}

// Media is the display asset attached to a storefront card. For video the
// storefront plays URL (an HLS manifest) over PosterURL, which is what gives
// the App Store-style shelf its motion.
type Media struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	URL       string `json:"url,omitempty"`
	PosterURL string `json:"poster_url,omitempty"`
	Blurhash  string `json:"blurhash,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

// Nutrition is per serving, precomputed by the database triggers.
type Nutrition struct {
	Kcal    float64 `json:"kcal"`
	Protein float64 `json:"protein"`
	Fat     float64 `json:"fat"`
	Carbs   float64 `json:"carbs"`
}

// Author is the byline a card needs, and the tap target that leads to the
// author's page. The id is what the client follows; the handle is what it
// shows and what a shared link carries.
type Author struct {
	ID        string `json:"id"`
	Handle    string `json:"handle"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Verified  bool   `json:"verified,omitempty"`
}

// Price is present only on paid listings.
type Price struct {
	Minor    int32  `json:"minor"`
	Currency string `json:"currency"`
}

// Card is one storefront tile.
type Card struct {
	ID              string    `json:"id"`
	Slug            string    `json:"slug"`
	Title           string    `json:"title"`
	Summary         string    `json:"summary"`
	CookTimeMinutes int       `json:"cook_time_minutes"`
	Difficulty      int       `json:"difficulty"`
	BaseServings    int       `json:"base_servings"`
	AccessTier      string    `json:"access_tier"`
	Price           *Price    `json:"price,omitempty"`
	Nutrition       Nutrition `json:"nutrition_per_serving"`
	Diets           []string  `json:"diets"`
	HasVideo        bool      `json:"has_video"`
	ImportCount     int64     `json:"import_count"`
	FavoriteCount   int64     `json:"favorite_count"`
	Rating          *float64  `json:"rating,omitempty"`
	RatingCount     int       `json:"rating_count"`
	PublishedAt     time.Time `json:"published_at"`
	Author          Author    `json:"author"`
	Media           *Media    `json:"media,omitempty"`

	// sort keys retained for cursor construction, not serialized
	rank float64
}

// Page is one page of results plus the cursor for the next one.
type Page struct {
	Items      []Card `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// Facets reports how many results each filter value would yield under the
// current query, so the UI can disable choices that lead nowhere.
type Facets struct {
	Total      int64            `json:"total"`
	Access     map[string]int64 `json:"access"`
	Categories map[string]int64 `json:"categories"`
	Cuisines   map[string]int64 `json:"cuisines"`
	Diets      map[string]int64 `json:"diets"`
	Video      map[string]int64 `json:"video"`
}

// Repo runs storefront queries.
type Repo struct {
	pool  *pgxpool.Pool
	media MediaURLResolver
}

func NewRepo(pool *pgxpool.Pool, media MediaURLResolver) *Repo {
	return &Repo{pool: pool, media: media}
}

// List returns one page of cards. viewerID may be empty for anonymous callers.
func (r *Repo) List(ctx context.Context, q Query, viewerID string) (Page, error) {
	sql, args := buildList(q, viewerID)

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return Page{}, fmt.Errorf("search: list: %w", err)
	}
	defer rows.Close()

	items := make([]Card, 0, q.Limit)
	for rows.Next() {
		card, err := scanCard(rows, r.media)
		if err != nil {
			return Page{}, err
		}
		items = append(items, card)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("search: iterate: %w", err)
	}

	page := Page{Items: items}
	// buildList asked for Limit+1 rows; the surplus row only signals that a
	// further page exists and is never returned to the client.
	if len(items) > q.Limit {
		page.Items = items[:q.Limit]
		page.HasMore = true
		last := page.Items[len(page.Items)-1]
		page.NextCursor = cursorFor(q.Sort, last).Encode()
	}
	return page, nil
}

// Facets counts the current result set per filter value.
func (r *Repo) Facets(ctx context.Context, q Query, viewerID string) (Facets, error) {
	sql, args := buildFacets(q, viewerID)

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return Facets{}, fmt.Errorf("search: facets: %w", err)
	}
	defer rows.Close()

	out := Facets{
		Access:     map[string]int64{},
		Categories: map[string]int64{},
		Cuisines:   map[string]int64{},
		Diets:      map[string]int64{},
		Video:      map[string]int64{},
	}
	for rows.Next() {
		var kind, key string
		var n int64
		if err := rows.Scan(&kind, &key, &n); err != nil {
			return Facets{}, fmt.Errorf("search: scan facet: %w", err)
		}
		switch kind {
		case "total":
			out.Total = n
		case "access":
			out.Access[key] = n
		case "category":
			out.Categories[key] = n
		case "cuisine":
			out.Cuisines[key] = n
		case "diet":
			out.Diets[key] = n
		case "video":
			out.Video[key] = n
		}
	}
	if err := rows.Err(); err != nil {
		return Facets{}, fmt.Errorf("search: iterate facets: %w", err)
	}
	return out, nil
}

// cursorFor builds the keyset position from the last row of a page. It must
// stay in step with orderBy and keysetPredicate.
func cursorFor(sort SortMode, last Card) paging.Cursor {
	c := paging.Cursor{Sort: string(sort), ID: last.ID}
	switch sort {
	case SortRelevance:
		c.Num = last.rank
	case SortPopular:
		c.Num = float64(last.ImportCount)
	case SortRating:
		if last.Rating != nil {
			c.Num = *last.Rating
		}
	case SortQuick:
		c.Num = float64(last.CookTimeMinutes)
	case SortLight:
		c.Num = last.Nutrition.Kcal
	default:
		c.Time = last.PublishedAt
	}
	return c
}

// rowScanner is the minimal surface of pgx.Rows that scanCard needs.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanCard reads one row. The destination order must match selectColumns in
// builder.go exactly; changing one without the other is a runtime error.
func scanCard(row rowScanner, media MediaURLResolver) (Card, error) {
	var (
		c          Card
		id         pgtype.UUID
		categoryID pgtype.Int2
		cuisineID  pgtype.Int2
		priceMinor pgtype.Int4
		currency   pgtype.Text
		rating     pgtype.Numeric
		authorID   pgtype.UUID
		avatarKey  pgtype.Text
		mediaID    pgtype.UUID
		mediaKind  pgtype.Text
		posterKey  pgtype.Text
		hlsKey     pgtype.Text
		storageKey pgtype.Text
		blurhash   pgtype.Text
		width      pgtype.Int4
		height     pgtype.Int4
	)

	if err := row.Scan(
		&id, &c.Slug, &c.Title, &c.Summary,
		&c.CookTimeMinutes, &c.Difficulty, &c.BaseServings,
		&categoryID, &cuisineID,
		&c.AccessTier, &priceMinor, &currency,
		&c.Nutrition.Kcal, &c.Nutrition.Protein, &c.Nutrition.Fat, &c.Nutrition.Carbs,
		&c.HasVideo, &c.ImportCount, &c.FavoriteCount,
		&rating, &c.RatingCount, &c.PublishedAt, &c.Diets,
		&authorID, &c.Author.Handle, &c.Author.Name, &c.Author.Verified, &avatarKey,
		&mediaID, &mediaKind, &posterKey, &hlsKey,
		&storageKey, &blurhash, &width, &height,
		&c.rank,
	); err != nil {
		return Card{}, fmt.Errorf("search: scan card: %w", err)
	}

	c.ID = uuidString(id)
	c.Author.ID = uuidString(authorID)
	if avatarKey.Valid {
		c.Author.AvatarURL = media.PublicURL(avatarKey.String)
	}
	if c.Diets == nil {
		c.Diets = []string{}
	}
	if priceMinor.Valid && currency.Valid {
		c.Price = &Price{Minor: priceMinor.Int32, Currency: currency.String}
	}
	if rating.Valid {
		if f, err := rating.Float64Value(); err == nil && f.Valid {
			v := f.Float64
			c.Rating = &v
		}
	}
	if mediaID.Valid {
		m := &Media{ID: uuidString(mediaID), Kind: mediaKind.String}
		if blurhash.Valid {
			m.Blurhash = blurhash.String
		}
		if width.Valid {
			m.Width = int(width.Int32)
		}
		if height.Valid {
			m.Height = int(height.Int32)
		}
		// Video plays from the HLS manifest with the poster as its still frame;
		// a photo is served directly. Originals are never exposed.
		if m.Kind == "video" {
			if hlsKey.Valid {
				m.URL = media.PublicURL(hlsKey.String)
			}
			if posterKey.Valid {
				m.PosterURL = media.PublicURL(posterKey.String)
			}
		} else if storageKey.Valid {
			m.URL = media.PublicURL(storageKey.String)
		}
		c.Media = m
	}
	return c, nil
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 36)
	pos := 0
	for i := 0; i < 16; i++ {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[pos] = '-'
			pos++
		}
		out[pos] = hexDigits[b[i]>>4]
		out[pos+1] = hexDigits[b[i]&0x0f]
		pos += 2
	}
	return string(out)
}

// ListOne returns a single card by id or slug, or nil when it is not visible to
// this viewer. It shares buildOne's projection with the feed so detail and
// listing can never disagree about visibility.
func (r *Repo) ListOne(ctx context.Context, _ Query, idOrSlug, viewerID string) (*Card, error) {
	sql, args := buildOne(idOrSlug, viewerID)

	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("search: list one: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("search: list one: %w", err)
		}
		return nil, nil
	}
	card, err := scanCard(rows, r.media)
	if err != nil {
		return nil, err
	}
	return &card, nil
}
