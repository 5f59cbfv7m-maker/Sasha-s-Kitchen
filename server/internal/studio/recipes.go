package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Units mirror the recipe_ingredients.unit CHECK constraint.
var validUnits = map[string]bool{"г": true, "мл": true, "шт": true}

// IngredientDraft carries the FULL product definition, not a reference to a
// shared catalogue.
//
// That is what lets an importing client create a product it has never seen
// without a second round trip, and it freezes the author's numbers so an edit
// elsewhere cannot silently restate a published recipe. The same shape is what
// internal/bundle hands the app.
type IngredientDraft struct {
	ProductName          string  `json:"product_name"`
	AmountPerBaseServing float64 `json:"amount_per_base_serving"`
	Unit                 string  `json:"unit"`
	GramsPerUnit         float64 `json:"grams_per_unit"`
	Category             string  `json:"category,omitempty"`
	KcalPer100           float64 `json:"kcal_per_100"`
	ProteinPer100        float64 `json:"protein_per_100"`
	FatPer100            float64 `json:"fat_per_100"`
	CarbsPer100          float64 `json:"carbs_per_100"`
	ShelfLifeDays        *int    `json:"shelf_life_days,omitempty"`
	LowStockThreshold    float64 `json:"low_stock_threshold,omitempty"`
	IsOptional           bool    `json:"is_optional,omitempty"`
	Note                 string  `json:"note,omitempty"`
}

// StepDraft is one instruction.
type StepDraft struct {
	Order int    `json:"order"`
	Text  string `json:"text"`
}

// RecipeDraft is a whole recipe as its author writes it.
type RecipeDraft struct {
	Title           string            `json:"title"`
	Summary         string            `json:"summary,omitempty"`
	Description     string            `json:"description,omitempty"`
	Slug            string            `json:"slug,omitempty"`
	CategorySlug    string            `json:"category,omitempty"`
	CuisineSlug     string            `json:"cuisine,omitempty"`
	Difficulty      int               `json:"difficulty"`
	CookTimeMinutes int               `json:"cook_time_minutes"`
	PrepTimeMinutes int               `json:"prep_time_minutes"`
	BaseServings    int               `json:"base_servings"`
	AccessTier      string            `json:"access_tier,omitempty"`
	PriceMinor      *int              `json:"price_minor,omitempty"`
	Currency        string            `json:"currency,omitempty"`
	Steps           []StepDraft       `json:"steps"`
	Ingredients     []IngredientDraft `json:"ingredients"`
	DietSlugs       []string          `json:"diets,omitempty"`
	MediaIDs        []string          `json:"media_ids,omitempty"`
}

// OwnRecipe is one row of the author's own list, in any state.
type OwnRecipe struct {
	ID          string     `json:"id"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	Rejected    string     `json:"rejected_reason,omitempty"`
	AccessTier  string     `json:"access_tier"`
	ImportCount int64      `json:"import_count"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ListOwnRecipes is backed by recipes_author_status_idx, which exists because
// every public read-path index deliberately excludes drafts.
func (r *Repo) ListOwnRecipes(ctx context.Context, authorID string) ([]OwnRecipe, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, slug, title, status, coalesce(rejected_reason,''),
		       access_tier, import_count, published_at, updated_at
		  FROM recipes
		 WHERE author_id = $1::uuid AND deleted_at IS NULL
		 ORDER BY updated_at DESC, id DESC`, authorID)
	if err != nil {
		return nil, fmt.Errorf("studio: list own recipes: %w", err)
	}
	defer rows.Close()

	out := []OwnRecipe{}
	for rows.Next() {
		var rec OwnRecipe
		var published pgtype.Timestamptz
		if err := rows.Scan(&rec.ID, &rec.Slug, &rec.Title, &rec.Status, &rec.Rejected,
			&rec.AccessTier, &rec.ImportCount, &published, &rec.UpdatedAt); err != nil {
			return nil, fmt.Errorf("studio: scan own recipe: %w", err)
		}
		if published.Valid {
			t := published.Time
			rec.PublishedAt = &t
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ValidateRecipe checks a draft, returning per-field Russian messages.
func (r *Repo) ValidateRecipe(ctx context.Context, authorID string, d *RecipeDraft) (map[string]string, error) {
	problems := map[string]string{}

	d.Title = strings.TrimSpace(d.Title)
	if n := len([]rune(d.Title)); n < 1 || n > 140 {
		problems["title"] = "Название от 1 до 140 символов"
	}
	d.Summary = strings.TrimSpace(d.Summary)
	if len([]rune(d.Summary)) > 400 {
		problems["summary"] = "Не более 400 символов"
	}
	if d.Difficulty < 1 || d.Difficulty > 3 {
		problems["difficulty"] = "Сложность — число от 1 до 3"
	}
	if d.CookTimeMinutes < 0 || d.CookTimeMinutes > 6000 {
		problems["cook_time_minutes"] = "Время приготовления от 0 до 6000 минут"
	}
	if d.PrepTimeMinutes < 0 || d.PrepTimeMinutes > 6000 {
		problems["prep_time_minutes"] = "Время подготовки от 0 до 6000 минут"
	}
	if d.BaseServings < 1 || d.BaseServings > 100 {
		problems["base_servings"] = "Число порций от 1 до 100"
	}

	if d.Slug == "" {
		d.Slug = slugify(d.Title)
	} else {
		d.Slug = slugify(d.Slug)
	}
	if d.Slug == "" {
		problems["slug"] = "Не удалось составить адрес: задайте его вручную"
	}

	// Paid recipes are modelled in the schema but have no purchase path yet, so
	// the API refuses to create one rather than publish something that says it
	// costs money and is handed out free.
	switch d.AccessTier {
	case "", "free":
		d.AccessTier = "free"
		d.PriceMinor, d.Currency = nil, ""
	case "paid":
		problems["access_tier"] = "Платные рецепты пока не поддерживаются"
	default:
		problems["access_tier"] = "Допустимые значения: free"
	}

	if len(d.Ingredients) == 0 {
		problems["ingredients"] = "Добавьте хотя бы один продукт"
	}
	if len(d.Ingredients) > 100 {
		problems["ingredients"] = "Не более 100 продуктов"
	}
	seen := map[string]bool{}
	for i := range d.Ingredients {
		ing := &d.Ingredients[i]
		field := fmt.Sprintf("ingredients[%d]", i)
		ing.ProductName = strings.TrimSpace(ing.ProductName)
		if n := len([]rune(ing.ProductName)); n < 1 || n > 120 {
			problems[field] = "Название продукта от 1 до 120 символов"
			continue
		}
		// The unique index is on the normalised key, so a duplicate would fail
		// at insert time with a constraint error rather than a useful message.
		key := normalizeKey(ing.ProductName)
		if seen[key] {
			problems[field] = "Этот продукт уже есть в списке"
			continue
		}
		seen[key] = true

		if ing.AmountPerBaseServing <= 0 {
			problems[field] = "Количество должно быть больше нуля"
		}
		if !validUnits[ing.Unit] {
			problems[field] = "Допустимые единицы: г, мл, шт"
		}
		if ing.GramsPerUnit <= 0 {
			// КБЖУ is computed from grams only, so a unit with no gram weight
			// would make the whole recipe's nutrition meaningless.
			ing.GramsPerUnit = 1
		}
		if ing.KcalPer100 < 0 || ing.ProteinPer100 < 0 || ing.FatPer100 < 0 || ing.CarbsPer100 < 0 {
			problems[field] = "КБЖУ не может быть отрицательным"
		}
	}

	for i := range d.Steps {
		d.Steps[i].Text = strings.TrimSpace(d.Steps[i].Text)
		if d.Steps[i].Text == "" {
			problems[fmt.Sprintf("steps[%d]", i)] = "Пустой шаг"
		}
		d.Steps[i].Order = i + 1
	}

	if len(d.MediaIDs) > 0 {
		var owned int
		if err := r.pool.QueryRow(ctx, `
			SELECT count(*) FROM media_assets
			 WHERE id = ANY($1::uuid[]) AND owner_id = $2::uuid`,
			d.MediaIDs, authorID).Scan(&owned); err != nil {
			return nil, fmt.Errorf("studio: verify recipe media: %w", err)
		}
		if owned != len(d.MediaIDs) {
			problems["media_ids"] = "Среди файлов есть не ваши"
		}
	}

	if len(problems) > 0 {
		return problems, nil
	}
	return nil, nil
}

// CreateRecipe stores a new draft with its ingredients, steps, diets and media
// in one transaction.
func (r *Repo) CreateRecipe(ctx context.Context, authorID string, d RecipeDraft) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("studio: create recipe begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	steps, err := json.Marshal(d.Steps)
	if err != nil {
		return "", fmt.Errorf("studio: encode steps: %w", err)
	}

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO recipes (author_id, slug, title, summary, description, steps,
		                     category_id, cuisine_id, difficulty,
		                     cook_time_minutes, prep_time_minutes, base_servings,
		                     access_tier, status)
		VALUES ($1::uuid, $2, $3, nullif($4,''), nullif($5,''), $6::jsonb,
		        (SELECT id FROM categories WHERE slug = nullif($7,'')),
		        (SELECT id FROM cuisines   WHERE slug = nullif($8,'')),
		        $9, $10, $11, $12, 'free', 'draft')
		RETURNING id::text`,
		authorID, d.Slug, d.Title, d.Summary, d.Description, string(steps),
		d.CategorySlug, d.CuisineSlug, int16(d.Difficulty),
		d.CookTimeMinutes, d.PrepTimeMinutes, d.BaseServings).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			return "", ErrDuplicateSlug
		}
		return "", fmt.Errorf("studio: create recipe: %w", err)
	}

	if err := writeChildren(ctx, tx, id, d); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("studio: create recipe commit: %w", err)
	}
	return id, nil
}

// UpdateRecipe replaces a recipe's contents.
//
// Children are deleted and rewritten rather than diffed: the triggers on
// recipe_ingredients recompute the derived columns either way, and a diff here
// would be more code with more ways to leave the derived state wrong.
func (r *Repo) UpdateRecipe(ctx context.Context, authorID, recipeID string, d RecipeDraft) error {
	policy, err := r.Policy(ctx, authorID)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("studio: update recipe begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	steps, err := json.Marshal(d.Steps)
	if err != nil {
		return fmt.Errorf("studio: encode steps: %w", err)
	}

	// Editing a live recipe sends a premoderated author back through review:
	// otherwise "publish something harmless, then rewrite it" walks straight
	// past moderation.
	tag, err := tx.Exec(ctx, `
		UPDATE recipes SET
		    slug = $3, title = $4, summary = nullif($5,''), description = nullif($6,''),
		    steps = $7::jsonb,
		    category_id = (SELECT id FROM categories WHERE slug = nullif($8,'')),
		    cuisine_id  = (SELECT id FROM cuisines   WHERE slug = nullif($9,'')),
		    difficulty = $10, cook_time_minutes = $11, prep_time_minutes = $12,
		    base_servings = $13,
		    status = CASE
		        WHEN status = 'published' AND $14 THEN 'review'
		        WHEN status = 'rejected' THEN 'draft'
		        ELSE status END,
		    published_at = CASE
		        WHEN status = 'published' AND $14 THEN NULL ELSE published_at END,
		    rejected_reason = NULL
		 WHERE id = $1::uuid AND author_id = $2::uuid AND deleted_at IS NULL`,
		recipeID, authorID, d.Slug, d.Title, d.Summary, d.Description, string(steps),
		d.CategorySlug, d.CuisineSlug, int16(d.Difficulty),
		d.CookTimeMinutes, d.PrepTimeMinutes, d.BaseServings, policy.Premoderate)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrDuplicateSlug
		}
		return fmt.Errorf("studio: update recipe: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}

	for _, t := range []string{"recipe_ingredients", "recipe_diets", "recipe_media"} {
		if _, err := tx.Exec(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE recipe_id = $1::uuid`, t), recipeID); err != nil {
			return fmt.Errorf("studio: clear %s: %w", t, err)
		}
	}
	if err := writeChildren(ctx, tx, recipeID, d); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("studio: update recipe commit: %w", err)
	}
	return nil
}

func writeChildren(ctx context.Context, tx pgx.Tx, recipeID string, d RecipeDraft) error {
	for i, ing := range d.Ingredients {
		if _, err := tx.Exec(ctx, `
			INSERT INTO recipe_ingredients (recipe_id, position, product_name,
			    amount_per_base_serving, unit, grams_per_unit, category,
			    kcal_per_100, protein_per_100, fat_per_100, carbs_per_100,
			    shelf_life_days, low_stock_threshold, is_optional, note)
			VALUES ($1::uuid, $2, $3, $4, $5, $6, nullif($7,''), $8, $9, $10, $11,
			        $12, $13, $14, nullif($15,''))`,
			recipeID, int32(i), ing.ProductName, ing.AmountPerBaseServing, ing.Unit,
			ing.GramsPerUnit, ing.Category, ing.KcalPer100, ing.ProteinPer100,
			ing.FatPer100, ing.CarbsPer100, ing.ShelfLifeDays,
			ing.LowStockThreshold, ing.IsOptional, ing.Note); err != nil {
			return fmt.Errorf("studio: write ingredient %q: %w", ing.ProductName, err)
		}
	}
	for _, slug := range d.DietSlugs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO recipe_diets (recipe_id, diet_tag_id)
			SELECT $1::uuid, id FROM diet_tags WHERE slug = $2
			ON CONFLICT DO NOTHING`, recipeID, slug); err != nil {
			return fmt.Errorf("studio: write diet %q: %w", slug, err)
		}
	}
	for i, mediaID := range d.MediaIDs {
		role := "gallery"
		if i == 0 {
			role = "hero"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO recipe_media (recipe_id, media_id, position, role)
			VALUES ($1::uuid, $2::uuid, $3, $4) ON CONFLICT DO NOTHING`,
			recipeID, mediaID, int32(i), role); err != nil {
			return fmt.Errorf("studio: attach media: %w", err)
		}
	}
	return nil
}

// DeleteRecipe soft-deletes, keeping moderation history and reports pointing at
// something real.
func (r *Repo) DeleteRecipe(ctx context.Context, authorID, recipeID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE recipes SET deleted_at = now(), status = 'archived'
		 WHERE id = $1::uuid AND author_id = $2::uuid AND deleted_at IS NULL`,
		recipeID, authorID)
	if err != nil {
		return fmt.Errorf("studio: delete recipe: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SubmitRecipe publishes or queues a recipe, under the same quota gate as posts.
func (r *Repo) SubmitRecipe(ctx context.Context, authorID, recipeID string) (*publishOutcome, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("studio: submit begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	policy, err := r.publishGate(ctx, tx, authorID)
	if err != nil {
		return nil, err
	}

	var status string
	var ingredients int
	err = tx.QueryRow(ctx, `
		SELECT r.status, (SELECT count(*) FROM recipe_ingredients i WHERE i.recipe_id = r.id)
		  FROM recipes r
		 WHERE r.id = $1::uuid AND r.author_id = $2::uuid AND r.deleted_at IS NULL`,
		recipeID, authorID).Scan(&status, &ingredients)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("studio: read recipe: %w", err)
	}
	if status != "draft" && status != "rejected" {
		return nil, ErrBadState
	}
	if ingredients == 0 {
		// A recipe with no products cannot be imported, which is the entire
		// point of publishing one here.
		return nil, ErrNoIngredients
	}

	next := "review"
	if !policy.Premoderate {
		next = "published"
	}
	var published pgtype.Timestamptz
	if err := tx.QueryRow(ctx, `
		UPDATE recipes
		   SET status = $3,
		       published_at = CASE WHEN $3 = 'published'
		                           THEN coalesce(published_at, now()) ELSE published_at END,
		       rejected_reason = NULL
		 WHERE id = $1::uuid AND author_id = $2::uuid AND deleted_at IS NULL
		 RETURNING published_at`, recipeID, authorID, next).Scan(&published); err != nil {
		return nil, fmt.Errorf("studio: submit recipe: %w", err)
	}

	if err := r.mod.LogEvent(ctx, "recipe", recipeID, authorID, authorID, "submitted", ""); err != nil {
		return nil, err
	}
	if next == "published" {
		if _, err := tx.Exec(ctx, `
			UPDATE user_stats SET recipe_count = recipe_count + 1
			 WHERE user_id = $1::uuid`, authorID); err != nil {
			return nil, fmt.Errorf("studio: bump recipe count: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("studio: submit commit: %w", err)
	}

	out := &publishOutcome{Status: next}
	if published.Valid {
		t := published.Time
		out.PublishedAt = &t
	}
	return out, nil
}

// ErrNoIngredients is returned when a recipe has nothing to import.
var ErrNoIngredients = errors.New("studio: recipe has no ingredients")

// normalizeKey mirrors sk_normalize_key() in SQL: lowercase, ё->е, trimmed,
// inner whitespace collapsed. It exists here only to catch duplicates before
// the unique index does, with a readable message.
func normalizeKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ё", "е")
	return strings.Join(strings.Fields(s), " ")
}
