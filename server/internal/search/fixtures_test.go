package search

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testPool connects to the database named by DATABASE_URL. Tests skip cleanly
// when it is unset so `go test ./...` still works on a machine without Postgres.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping database-backed test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// stubResolver stands in for the CDN so tests assert on stable strings.
type stubResolver struct{}

func (stubResolver) PublicURL(key string) string { return "https://cdn.test/" + key }

type ingredientSpec struct {
	name     string
	amount   float64
	unit     string
	gpu      float64
	kcal     float64
	optional bool
}

type recipeSpec struct {
	slug        string
	title       string
	summary     string
	category    string
	cuisine     string
	diets       []string
	difficulty  int
	cookTime    int
	servings    int
	access      string
	priceMinor  *int32
	imports     int64
	rating      *float64
	published   time.Time
	ingredients []ingredientSpec
}

// seedFixtures wipes the store tables and loads a small but deliberately varied
// catalogue: enough axes to exercise every filter without being unreadable.
func seedFixtures(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	// Order matters only for readability; TRUNCATE ... CASCADE handles the rest.
	if _, err := pool.Exec(ctx, `
		TRUNCATE recipes, users, categories, cuisines, diet_tags,
		         collections, collection_items, media_assets, recipe_imports,
		         favorites, ratings, reports, user_blocks, moderation_events
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	for _, c := range []struct{ slug, title string }{
		{"breakfast", "Завтрак"}, {"soup", "Суп"}, {"dessert", "Десерт"}, {"main", "Основное"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO categories (slug, title) VALUES ($1,$2)`, c.slug, c.title); err != nil {
			t.Fatalf("seed category: %v", err)
		}
	}
	for _, c := range []struct{ slug, title string }{
		{"russian", "Русская"}, {"italian", "Итальянская"}, {"japanese", "Японская"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO cuisines (slug, title) VALUES ($1,$2)`, c.slug, c.title); err != nil {
			t.Fatalf("seed cuisine: %v", err)
		}
	}
	for _, d := range []struct{ slug, title string }{
		{"vegetarian", "Вегетарианское"}, {"gluten_free", "Без глютена"}, {"lactose_free", "Без лактозы"},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO diet_tags (slug, title) VALUES ($1,$2)`, d.slug, d.title); err != nil {
			t.Fatalf("seed diet: %v", err)
		}
	}

	var authorID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (handle, display_name, is_author)
		VALUES ('chef_anna','Анна',true) RETURNING id::text`).Scan(&authorID); err != nil {
		t.Fatalf("seed author: %v", err)
	}
	var otherID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (handle, display_name, is_author)
		VALUES ('chef_boris','Борис',true) RETURNING id::text`).Scan(&otherID); err != nil {
		t.Fatalf("seed other author: %v", err)
	}

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	price := int32(19900)

	specs := []recipeSpec{
		{
			slug: "omlet", title: "Омлет с молоком", summary: "Быстрый завтрак",
			category: "breakfast", cuisine: "russian", diets: []string{"vegetarian", "gluten_free"},
			difficulty: 1, cookTime: 10, servings: 2, access: "free",
			imports: 500, published: base,
			ingredients: []ingredientSpec{
				{name: "Яйцо", amount: 3, unit: "шт", gpu: 55, kcal: 157},
				{name: "Молоко", amount: 100, unit: "мл", gpu: 1, kcal: 60},
				{name: "Зелёный лук", amount: 10, unit: "г", gpu: 1, kcal: 32, optional: true},
			},
		},
		{
			slug: "borsch", title: "Борщ", summary: "Классический суп",
			category: "soup", cuisine: "russian", diets: []string{},
			difficulty: 3, cookTime: 120, servings: 6, access: "free",
			imports: 900, published: base.Add(24 * time.Hour),
			ingredients: []ingredientSpec{
				{name: "Свёкла", amount: 300, unit: "г", gpu: 1, kcal: 42},
				{name: "Говядина", amount: 500, unit: "г", gpu: 1, kcal: 250},
				{name: "Капуста", amount: 200, unit: "г", gpu: 1, kcal: 25},
			},
		},
		{
			slug: "pasta-carbonara", title: "Паста карбонара", summary: "Классика Рима",
			category: "main", cuisine: "italian", diets: []string{},
			difficulty: 2, cookTime: 25, servings: 2, access: "paid", priceMinor: &price,
			imports: 300, published: base.Add(48 * time.Hour),
			ingredients: []ingredientSpec{
				{name: "Спагетти", amount: 200, unit: "г", gpu: 1, kcal: 350},
				{name: "Яйцо", amount: 2, unit: "шт", gpu: 55, kcal: 157},
				{name: "Бекон", amount: 100, unit: "г", gpu: 1, kcal: 500},
			},
		},
		{
			slug: "salat-ovoshnoy", title: "Овощной салат", summary: "Лёгкий и свежий",
			category: "main", cuisine: "russian",
			diets:      []string{"vegetarian", "gluten_free", "lactose_free"},
			difficulty: 1, cookTime: 5, servings: 2, access: "free",
			imports: 120, published: base.Add(72 * time.Hour),
			ingredients: []ingredientSpec{
				{name: "Огурец", amount: 100, unit: "г", gpu: 1, kcal: 15},
				{name: "Помидор", amount: 100, unit: "г", gpu: 1, kcal: 18},
			},
		},
		{
			slug: "tiramisu", title: "Тирамису", summary: "Десерт с кофе",
			category: "dessert", cuisine: "italian", diets: []string{"vegetarian"},
			difficulty: 3, cookTime: 40, servings: 8, access: "paid", priceMinor: &price,
			imports: 50, published: base.Add(96 * time.Hour),
			ingredients: []ingredientSpec{
				{name: "Маскарпоне", amount: 500, unit: "г", gpu: 1, kcal: 430},
				{name: "Яйцо", amount: 4, unit: "шт", gpu: 55, kcal: 157},
				{name: "Арахис", amount: 20, unit: "г", gpu: 1, kcal: 567, optional: true},
			},
		},
		{
			slug: "sup-miso", title: "Мисо-суп", summary: "Японский бульон",
			category: "soup", cuisine: "japanese", diets: []string{"vegetarian", "lactose_free"},
			difficulty: 1, cookTime: 15, servings: 2, access: "free",
			imports: 200, published: base.Add(120 * time.Hour),
			ingredients: []ingredientSpec{
				{name: "Мисо паста", amount: 40, unit: "г", gpu: 1, kcal: 199},
				{name: "Тофу", amount: 150, unit: "г", gpu: 1, kcal: 76},
			},
		},
	}

	for _, s := range specs {
		insertRecipe(t, pool, authorID, s)
	}

	// A recipe by the second author, used by the blocking test.
	insertRecipe(t, pool, otherID, recipeSpec{
		slug: "blini", title: "Блины", summary: "На молоке",
		category: "breakfast", cuisine: "russian", diets: []string{"vegetarian"},
		difficulty: 1, cookTime: 30, servings: 4, access: "free",
		imports: 10, published: base.Add(144 * time.Hour),
		ingredients: []ingredientSpec{
			{name: "Мука", amount: 200, unit: "г", gpu: 1, kcal: 340},
			{name: "Молоко", amount: 500, unit: "мл", gpu: 1, kcal: 60},
		},
	})

	// A draft must never appear in the storefront.
	insertRecipeStatus(t, pool, authorID, recipeSpec{
		slug: "draft-only", title: "Черновик", category: "main", cuisine: "russian",
		difficulty: 1, cookTime: 10, servings: 2, access: "free", published: base,
		ingredients: []ingredientSpec{{name: "Соль", amount: 5, unit: "г", gpu: 1, kcal: 0}},
	}, "draft")
}

func insertRecipe(t *testing.T, pool *pgxpool.Pool, authorID string, s recipeSpec) {
	t.Helper()
	insertRecipeStatus(t, pool, authorID, s, "published")
}

func insertRecipeStatus(t *testing.T, pool *pgxpool.Pool, authorID string, s recipeSpec, status string) {
	t.Helper()
	ctx := context.Background()

	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO recipes (author_id, slug, title, summary, category_id, cuisine_id,
		                     difficulty, cook_time_minutes, base_servings, access_tier,
		                     price_minor, currency, status, published_at, import_count)
		VALUES ($1,$2,$3,$4,
		        (SELECT id FROM categories WHERE slug=$5),
		        (SELECT id FROM cuisines   WHERE slug=$6),
		        $7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id::text`,
		authorID, s.slug, s.title, s.summary, s.category, s.cuisine,
		s.difficulty, s.cookTime, s.servings, s.access,
		s.priceMinor, currencyFor(s.priceMinor), status,
		publishedFor(status, s.published), s.imports).Scan(&id)
	if err != nil {
		t.Fatalf("insert recipe %s: %v", s.slug, err)
	}

	for i, ing := range s.ingredients {
		if _, err := pool.Exec(ctx, `
			INSERT INTO recipe_ingredients
			    (recipe_id, position, product_name, amount_per_base_serving, unit,
			     grams_per_unit, kcal_per_100, is_optional)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			id, i, ing.name, ing.amount, ing.unit, ing.gpu, ing.kcal, ing.optional); err != nil {
			t.Fatalf("insert ingredient %s/%s: %v", s.slug, ing.name, err)
		}
	}
	for _, d := range s.diets {
		if _, err := pool.Exec(ctx, `
			INSERT INTO recipe_diets (recipe_id, diet_tag_id)
			VALUES ($1, (SELECT id FROM diet_tags WHERE slug=$2))`, id, d); err != nil {
			t.Fatalf("insert diet %s/%s: %v", s.slug, d, err)
		}
	}
}

func currencyFor(price *int32) any {
	if price == nil {
		return nil
	}
	return "RUB"
}

func publishedFor(status string, at time.Time) any {
	if status != "published" {
		return nil
	}
	return at
}

// slugs is a readable assertion helper.
func slugs(items []Card) []string {
	out := make([]string, len(items))
	for i, c := range items {
		out[i] = c.Slug
	}
	return out
}

// mustParse builds a Query from a raw query string, the same way the HTTP
// layer will, so the tests exercise parsing and validation too.
func mustParse(t *testing.T, raw string) Query {
	t.Helper()
	vals, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("parse query %q: %v", raw, err)
	}
	q, err := ParseQuery(vals)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", raw, err)
	}
	return q
}

func fmtCards(items []Card) string { return fmt.Sprintf("%v", slugs(items)) }
