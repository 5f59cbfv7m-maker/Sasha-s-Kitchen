// Package storefront renders the editorial front page and recipe detail.
//
// The shape is modelled on the App Store: a vertical stack of shelves, each
// with a title and a row of cards, where a card leads with video when the
// author supplied one and a photo otherwise.
package storefront

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
)

// ErrNotFound is returned for a missing or unavailable recipe.
var ErrNotFound = errors.New("storefront: not found")

// Shelf is one merchandised row.
type Shelf struct {
	Slug     string        `json:"slug"`
	Title    string        `json:"title"`
	Subtitle string        `json:"subtitle,omitempty"`
	Kind     string        `json:"kind"` // hero | shelf | grid | spotlight
	Items    []search.Card `json:"items"`
}

// Front is the whole front page.
type Front struct {
	Shelves []Shelf `json:"shelves"`
}

// Detail is the recipe page behind a card. For a paid listing the ingredient
// list and steps are withheld: the teaser shows media, description and price.
type Detail struct {
	search.Card
	Description string             `json:"description,omitempty"`
	PrepTime    int                `json:"prep_time_minutes"`
	Category    string             `json:"category,omitempty"`
	Cuisine     string             `json:"cuisine,omitempty"`
	Steps       []Step             `json:"steps,omitempty"`
	Ingredients []DetailIngredient `json:"ingredients,omitempty"`
	Gallery     []search.Media     `json:"gallery"`
	Locked      bool               `json:"locked"`
	CanImport   bool               `json:"can_import"`
}

// Step is one instruction in the detail view.
type Step struct {
	Order int    `json:"order"`
	Text  string `json:"text"`
}

// DetailIngredient is a display row, already scaled to base servings.
type DetailIngredient struct {
	Name     string  `json:"name"`
	Key      string  `json:"key"`
	Amount   float64 `json:"amount"`
	Unit     string  `json:"unit"`
	Optional bool    `json:"optional"`
	Note     string  `json:"note,omitempty"`
}

// Repo renders storefront views.
type Repo struct {
	pool   *pgxpool.Pool
	search *search.Repo
	media  search.MediaURLResolver
}

func NewRepo(pool *pgxpool.Pool, s *search.Repo, media search.MediaURLResolver) *Repo {
	return &Repo{pool: pool, search: s, media: media}
}

// Front renders the active shelves.
//
// One query per shelf is deliberate: shelves are few, each is a plain indexed
// lookup, and the whole response is cached. A single clever query returning all
// shelves at once would be harder to read and no faster in practice.
func (r *Repo) Front(ctx context.Context, viewerID string, perShelf int) (Front, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, slug, title, coalesce(subtitle, ''), kind
		  FROM collections
		 WHERE is_active
		   AND (starts_at IS NULL OR starts_at <= now())
		   AND (ends_at   IS NULL OR ends_at   >  now())
		 ORDER BY position ASC, id ASC
		 LIMIT 24`)
	if err != nil {
		return Front{}, fmt.Errorf("storefront: load collections: %w", err)
	}

	type collection struct{ id, slug, title, subtitle, kind string }
	var collections []collection
	for rows.Next() {
		var c collection
		if err := rows.Scan(&c.id, &c.slug, &c.title, &c.subtitle, &c.kind); err != nil {
			rows.Close()
			return Front{}, fmt.Errorf("storefront: scan collection: %w", err)
		}
		collections = append(collections, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Front{}, fmt.Errorf("storefront: iterate collections: %w", err)
	}

	front := Front{Shelves: []Shelf{}}
	for _, c := range collections {
		q := search.Query{
			Access:       search.AccessAny,
			Sort:         search.SortNew,
			Limit:        perShelf,
			CollectionID: c.id,
		}
		page, err := r.search.List(ctx, q, viewerID)
		if err != nil {
			return Front{}, fmt.Errorf("storefront: shelf %s: %w", c.slug, err)
		}
		// An empty shelf is merchandising noise; drop it rather than render it.
		if len(page.Items) == 0 {
			continue
		}
		front.Shelves = append(front.Shelves, Shelf{
			Slug: c.slug, Title: c.title, Subtitle: c.subtitle,
			Kind: c.kind, Items: page.Items,
		})
	}
	return front, nil
}

// Detail renders one recipe page. idOrSlug accepts either form so links stay
// readable without a second lookup table.
func (r *Repo) Detail(ctx context.Context, idOrSlug, viewerID string) (*Detail, error) {
	q := search.Query{Access: search.AccessAny, Sort: search.SortNew, Limit: 1}
	page, err := r.search.ListOne(ctx, q, idOrSlug, viewerID)
	if err != nil {
		return nil, err
	}
	if page == nil {
		return nil, ErrNotFound
	}

	d := &Detail{Card: *page, Gallery: []search.Media{}}

	var (
		description pgtype.Text
		category    pgtype.Text
		cuisine     pgtype.Text
		stepsRaw    []byte
		owned       bool
	)
	err = r.pool.QueryRow(ctx, `
		SELECT r.description, c.slug, cu.slug, r.steps, r.prep_time_minutes,
		       ($2 <> '' AND EXISTS (
		           SELECT 1 FROM recipe_imports ri
		            WHERE ri.recipe_id = r.id AND ri.user_id = $2::uuid)) AS owned
		  FROM recipes r
		  LEFT JOIN categories c  ON c.id  = r.category_id
		  LEFT JOIN cuisines  cu ON cu.id = r.cuisine_id
		 WHERE r.id = $1::uuid`, d.Card.ID, viewerID).Scan(
		&description, &category, &cuisine, &stepsRaw, &d.PrepTime, &owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("storefront: detail: %w", err)
	}
	d.Description = description.String
	d.Category = category.String
	d.Cuisine = cuisine.String

	// A paid listing shows the shop window only: media, description, price.
	// Ingredients and steps are the goods, and stay behind the counter.
	d.Locked = d.Card.AccessTier == "paid" && !owned
	d.CanImport = !d.Locked

	if !d.Locked {
		d.Steps = decodeSteps(stepsRaw)
		if d.Ingredients, err = r.ingredients(ctx, d.Card.ID); err != nil {
			return nil, err
		}
	}
	if d.Gallery, err = r.gallery(ctx, d.Card.ID); err != nil {
		return nil, err
	}
	return d, nil
}

func (r *Repo) ingredients(ctx context.Context, recipeID string) ([]DetailIngredient, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT product_name, product_key, amount_per_base_serving, unit,
		       is_optional, coalesce(note, '')
		  FROM recipe_ingredients
		 WHERE recipe_id = $1::uuid
		 ORDER BY position ASC, product_key ASC`, recipeID)
	if err != nil {
		return nil, fmt.Errorf("storefront: ingredients: %w", err)
	}
	defer rows.Close()

	out := []DetailIngredient{}
	for rows.Next() {
		var i DetailIngredient
		if err := rows.Scan(&i.Name, &i.Key, &i.Amount, &i.Unit, &i.Optional, &i.Note); err != nil {
			return nil, fmt.Errorf("storefront: scan ingredient: %w", err)
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (r *Repo) gallery(ctx context.Context, recipeID string) ([]search.Media, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.id::text, m.kind, coalesce(m.poster_key,''), coalesce(m.hls_key,''),
		       coalesce(m.storage_key,''), coalesce(m.blurhash,''),
		       coalesce(m.width,0), coalesce(m.height,0)
		  FROM recipe_media rm
		  JOIN media_assets m ON m.id = rm.media_id
		 WHERE rm.recipe_id = $1::uuid AND m.status = 'ready' AND rm.role <> 'step'
		 ORDER BY (rm.role = 'hero') DESC, rm.position ASC, m.id ASC
		 LIMIT 20`, recipeID)
	if err != nil {
		return nil, fmt.Errorf("storefront: gallery: %w", err)
	}
	defer rows.Close()

	out := []search.Media{}
	for rows.Next() {
		var (
			m                              search.Media
			poster, hls, storage, blurhash string
		)
		if err := rows.Scan(&m.ID, &m.Kind, &poster, &hls, &storage, &blurhash,
			&m.Width, &m.Height); err != nil {
			return nil, fmt.Errorf("storefront: scan media: %w", err)
		}
		m.Blurhash = blurhash
		if m.Kind == "video" {
			if hls != "" {
				m.URL = r.media.PublicURL(hls)
			}
			if poster != "" {
				m.PosterURL = r.media.PublicURL(poster)
			}
		} else if storage != "" {
			m.URL = r.media.PublicURL(storage)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func decodeSteps(raw []byte) []Step {
	var objects []struct {
		Order int    `json:"order"`
		Text  string `json:"text"`
	}
	if len(raw) == 0 {
		return []Step{}
	}
	if err := unmarshal(raw, &objects); err == nil {
		out := make([]Step, 0, len(objects))
		for i, o := range objects {
			if o.Order == 0 {
				o.Order = i
			}
			out = append(out, Step{Order: o.Order, Text: o.Text})
		}
		return out
	}
	var plain []string
	if err := unmarshal(raw, &plain); err == nil {
		out := make([]Step, 0, len(plain))
		for i, s := range plain {
			out = append(out, Step{Order: i, Text: s})
		}
		return out
	}
	return []Step{}
}
