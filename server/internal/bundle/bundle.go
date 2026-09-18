// Package bundle produces the self-contained recipe payload a client imports.
//
// The contract's defining property: a bundle carries the FULL definition of
// every product it references, not a reference into some shared catalogue. An
// importing client can therefore create products it has never seen, compute
// КБЖУ locally, and push whatever it lacks into its shopping list, all without
// a second round trip and without the server knowing anything about that
// client's catalogue.
//
// The format is deliberately plain JSON with no Apple-specific types: Android
// and Windows clients are planned and must be able to consume exactly this.
package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SchemaVersion is the wire-format version. Bump it only for a breaking change;
// clients are expected to refuse a version they do not know.
const SchemaVersion = 1

// ErrNotFound is returned when the recipe is absent, unpublished, or paid and
// unowned. The caller maps it to a 404.
var ErrNotFound = errors.New("bundle: recipe not available")

// Nutrition is per 100 g for a product, and per serving for a recipe.
type Nutrition struct {
	Kcal    float64 `json:"kcal"`
	Protein float64 `json:"protein"`
	Fat     float64 `json:"fat"`
	Carbs   float64 `json:"carbs"`
}

// Product is a complete catalogue entry, enough for a client to create it.
type Product struct {
	// Key is the normalised matching key. Clients MUST match on this rather
	// than on Name, and MUST normalise their own catalogue the same way:
	// lowercase, ё->е, trimmed, inner whitespace collapsed.
	Key               string    `json:"key"`
	Name              string    `json:"name"`
	Category          string    `json:"category,omitempty"`
	Unit              string    `json:"unit"` // г | мл | шт
	GramsPerUnit      float64   `json:"grams_per_unit"`
	NutritionPer100   Nutrition `json:"nutrition_per_100"`
	ShelfLifeDays     *int      `json:"shelf_life_days,omitempty"`
	LowStockThreshold float64   `json:"low_stock_threshold"`
}

// Ingredient ties a product key to an amount.
type Ingredient struct {
	ProductKey string `json:"product_key"`
	// Amount is for the WHOLE recipe at BaseServings, matching the client's
	// existing RecipeIngredient semantics. Scale by servings/base_servings.
	Amount   float64 `json:"amount_per_base_serving"`
	Unit     string  `json:"unit"`
	Position int     `json:"position"`
	Optional bool    `json:"optional"`
	Note     string  `json:"note,omitempty"`
}

// Author is the byline carried with an imported recipe so provenance survives.
type Author struct {
	Handle string `json:"handle"`
	Name   string `json:"name"`
}

// Step is one instruction.
type Step struct {
	Order int    `json:"order"`
	Text  string `json:"text"`
}

// Recipe is the imported recipe itself.
type Recipe struct {
	// SourceID lets a client recognise a re-import as an update rather than a
	// duplicate. It is the store's recipe id and is stable forever.
	SourceID        string   `json:"source_id"`
	Slug            string   `json:"slug"`
	Title           string   `json:"title"`
	Summary         string   `json:"summary,omitempty"`
	Description     string   `json:"description,omitempty"`
	Steps           []Step   `json:"steps"`
	CookTimeMinutes int      `json:"cook_time_minutes"`
	PrepTimeMinutes int      `json:"prep_time_minutes"`
	BaseServings    int      `json:"base_servings"`
	Difficulty      int      `json:"difficulty"`
	Category        string   `json:"category,omitempty"`
	Cuisine         string   `json:"cuisine,omitempty"`
	Diets           []string `json:"diets"`
	Author          Author   `json:"author"`
	PublishedAt     string   `json:"published_at,omitempty"`
}

// Bundle is the whole payload.
type Bundle struct {
	SchemaVersion       int          `json:"schema_version"`
	Recipe              Recipe       `json:"recipe"`
	Products            []Product    `json:"products"`
	Ingredients         []Ingredient `json:"ingredients"`
	NutritionPerServing Nutrition    `json:"nutrition_per_serving"`
}

// Repo builds bundles.
type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// Build assembles the bundle for a published recipe.
//
// A paid recipe is refused unless viewerID owns it. That check lives here
// rather than in the handler because this is the endpoint that hands over the
// actual content: a paywall enforced only at the edge is not a paywall.
func (r *Repo) Build(ctx context.Context, recipeID, viewerID string) (*Bundle, error) {
	var (
		b           Bundle
		id          pgtype.UUID
		summary     pgtype.Text
		description pgtype.Text
		category    pgtype.Text
		cuisine     pgtype.Text
		publishedAt pgtype.Timestamptz
		stepsRaw    []byte
		accessTier  string
		owned       bool
	)

	err := r.pool.QueryRow(ctx, `
		SELECT r.id, r.slug, r.title, r.summary, r.description, r.steps,
		       r.cook_time_minutes, r.prep_time_minutes, r.base_servings, r.difficulty,
		       c.slug, cu.slug, r.diet_slugs, r.published_at, r.access_tier,
		       r.kcal_per_serving, r.protein_per_serving, r.fat_per_serving, r.carbs_per_serving,
		       u.handle, u.display_name,
		       ($2 <> '' AND EXISTS (
		            SELECT 1 FROM recipe_imports ri
		             WHERE ri.recipe_id = r.id AND ri.user_id = $2::uuid)) AS owned
		  FROM recipes r
		  JOIN users u ON u.id = r.author_id
		  LEFT JOIN categories c ON c.id = r.category_id
		  LEFT JOIN cuisines cu ON cu.id = r.cuisine_id
		 WHERE r.id = $1::uuid
		   AND r.status = 'published'
		   AND r.deleted_at IS NULL
		   AND u.status = 'active'`, recipeID, viewerID).Scan(
		&id, &b.Recipe.Slug, &b.Recipe.Title, &summary, &description, &stepsRaw,
		&b.Recipe.CookTimeMinutes, &b.Recipe.PrepTimeMinutes, &b.Recipe.BaseServings,
		&b.Recipe.Difficulty, &category, &cuisine, &b.Recipe.Diets, &publishedAt, &accessTier,
		&b.NutritionPerServing.Kcal, &b.NutritionPerServing.Protein,
		&b.NutritionPerServing.Fat, &b.NutritionPerServing.Carbs,
		&b.Recipe.Author.Handle, &b.Recipe.Author.Name, &owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("bundle: load recipe: %w", err)
	}

	// Paid content is withheld until it is owned. Payments are not implemented
	// yet, so today this simply means paid bundles are never handed out.
	if accessTier == "paid" && !owned {
		return nil, ErrNotFound
	}

	b.SchemaVersion = SchemaVersion
	b.Recipe.SourceID = uuidString(id)
	b.Recipe.Summary = summary.String
	b.Recipe.Description = description.String
	b.Recipe.Category = category.String
	b.Recipe.Cuisine = cuisine.String
	if publishedAt.Valid {
		b.Recipe.PublishedAt = publishedAt.Time.UTC().Format("2006-01-02T15:04:05Z")
	}
	if b.Recipe.Diets == nil {
		b.Recipe.Diets = []string{}
	}
	b.Recipe.Steps = decodeSteps(stepsRaw)

	rows, err := r.pool.Query(ctx, `
		SELECT product_key, product_name, category, unit, grams_per_unit,
		       kcal_per_100, protein_per_100, fat_per_100, carbs_per_100,
		       shelf_life_days, low_stock_threshold,
		       amount_per_base_serving, position, is_optional, note
		  FROM recipe_ingredients
		 WHERE recipe_id = $1::uuid
		 ORDER BY position ASC, product_key ASC`, recipeID)
	if err != nil {
		return nil, fmt.Errorf("bundle: load ingredients: %w", err)
	}
	defer rows.Close()

	b.Products = []Product{}
	b.Ingredients = []Ingredient{}
	for rows.Next() {
		var (
			p     Product
			ing   Ingredient
			cat   pgtype.Text
			shelf pgtype.Int4
			note  pgtype.Text
		)
		if err := rows.Scan(&p.Key, &p.Name, &cat, &p.Unit, &p.GramsPerUnit,
			&p.NutritionPer100.Kcal, &p.NutritionPer100.Protein,
			&p.NutritionPer100.Fat, &p.NutritionPer100.Carbs,
			&shelf, &p.LowStockThreshold,
			&ing.Amount, &ing.Position, &ing.Optional, &note); err != nil {
			return nil, fmt.Errorf("bundle: scan ingredient: %w", err)
		}
		p.Category = cat.String
		if shelf.Valid {
			days := int(shelf.Int32)
			p.ShelfLifeDays = &days
		}
		ing.ProductKey = p.Key
		ing.Unit = p.Unit
		ing.Note = note.String

		b.Products = append(b.Products, p)
		b.Ingredients = append(b.Ingredients, ing)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("bundle: iterate ingredients: %w", err)
	}
	if len(b.Ingredients) == 0 {
		// A recipe with no ingredients cannot be imported meaningfully; the
		// client would create an empty recipe and nothing for the shopping list.
		return nil, ErrNotFound
	}
	return &b, nil
}

// decodeSteps tolerates both shapes the steps column has carried: a JSON array
// of objects and a plain array of strings.
func decodeSteps(raw []byte) []Step {
	if len(raw) == 0 {
		return []Step{}
	}
	var objects []struct {
		Order int    `json:"order"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal(raw, &objects); err == nil {
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
	if err := json.Unmarshal(raw, &plain); err == nil {
		out := make([]Step, 0, len(plain))
		for i, s := range plain {
			out = append(out, Step{Order: i, Text: s})
		}
		return out
	}
	return []Step{}
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 36)
	pos := 0
	for i := 0; i < 16; i++ {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[pos] = '-'
			pos++
		}
		out[pos] = hexDigits[u.Bytes[i]>>4]
		out[pos+1] = hexDigits[u.Bytes[i]&0x0f]
		pos += 2
	}
	return string(out)
}
