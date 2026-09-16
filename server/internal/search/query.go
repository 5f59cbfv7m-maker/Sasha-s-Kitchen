// Package search turns storefront filter parameters into indexed SQL.
//
// Every filter here is backed by a specific index declared in
// migrations/0002_recipes.sql. Adding a filter without adding its index is how
// a storefront becomes slow, so keep the two in step.
package search

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/httpx"
)

// SortMode names an ordering. Each mode maps to one partial index and one
// keyset cursor shape; see builder.go.
type SortMode string

const (
	SortRelevance SortMode = "relevance" // only meaningful with a text query
	SortNew       SortMode = "new"
	SortPopular   SortMode = "popular"
	SortRating    SortMode = "rating"
	SortQuick     SortMode = "quick" // shortest cooking time first
	SortLight     SortMode = "light" // fewest kcal per serving first
)

// AccessFilter selects free, paid, or both.
type AccessFilter string

const (
	AccessAny  AccessFilter = "any"
	AccessFree AccessFilter = "free"
	AccessPaid AccessFilter = "paid"
)

// Limits that keep a hostile or careless client from turning one request into
// a table scan.
const (
	DefaultLimit       = 20
	MaxLimit           = 50
	MaxTextLength      = 200
	MaxIngredientTerms = 20
	MaxPantryTerms     = 500
	MaxFacetTerms      = 20
)

// Range is an inclusive numeric filter; either bound may be absent.
type Range struct {
	Min *float64
	Max *float64
}

func (r Range) isSet() bool { return r.Min != nil || r.Max != nil }

// Query is a fully validated storefront query. Construct it with ParseQuery so
// the invariants the builder relies on are guaranteed to hold.
type Query struct {
	Text string

	CategorySlugs []string
	CuisineSlugs  []string
	Difficulties  []int
	DietSlugs     []string // AND: a recipe must carry every requested tag

	CookTime Range
	Kcal     Range
	Protein  Range
	Fat      Range
	Carbs    Range

	// IncludeIngredients requires every listed key to be present.
	IncludeIngredients []string
	// ExcludeIngredients rejects a recipe carrying any listed key. This is the
	// allergy filter, so it checks ALL ingredients including optional ones.
	ExcludeIngredients []string

	// Pantry is the caller's stock, normalised. With CanCookNow it keeps only
	// recipes whose required ingredients are a subset of it.
	Pantry     []string
	CanCookNow bool

	Access       AccessFilter
	HasVideo     *bool
	AuthorHandle string
	CollectionID string

	Sort   SortMode
	Cursor *Cursor
	Limit  int
}

// NormalizeKey mirrors sk_normalize_key() in SQL byte for byte: lowercase,
// ё->е, trimmed, inner whitespace collapsed to single spaces.
//
// If you change this, change the SQL function in the same commit. A divergence
// does not error — it silently stops matching ingredients, which is far worse.
func NormalizeKey(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case 'Ё':
			return 'Е'
		case 'ё':
			return 'е'
		}
		return r
	}, s)
	s = strings.ToLower(s)

	var b strings.Builder
	b.Grow(len(s))
	space := false
	started := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && started {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
		space = false
		started = true
	}
	return b.String()
}

// ParseQuery reads and validates filters from a query string, returning
// httpx.Validation with per-field Russian messages when anything is off.
func ParseQuery(v url.Values) (Query, error) {
	q := Query{
		Access: AccessAny,
		Sort:   SortNew,
		Limit:  DefaultLimit,
	}
	problems := map[string]string{}

	q.Text = strings.TrimSpace(v.Get("q"))
	if len([]rune(q.Text)) > MaxTextLength {
		problems["q"] = fmt.Sprintf("Запрос длиннее %d символов", MaxTextLength)
	}

	q.CategorySlugs = parseSlugList(v, "category", "category[]", problems, "category")
	q.CuisineSlugs = parseSlugList(v, "cuisine", "cuisine[]", problems, "cuisine")
	q.DietSlugs = parseSlugList(v, "diet", "diet[]", problems, "diet")

	for _, raw := range splitList(v, "difficulty", "difficulty[]") {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 3 {
			problems["difficulty"] = "Сложность — число от 1 до 3"
			break
		}
		q.Difficulties = append(q.Difficulties, n)
	}

	q.CookTime = parseRange(v, "cook_time_min", "cook_time_max", problems, "cook_time")
	q.Kcal = parseRange(v, "kcal_min", "kcal_max", problems, "kcal")
	q.Protein = parseRange(v, "protein_min", "protein_max", problems, "protein")
	q.Fat = parseRange(v, "fat_min", "fat_max", problems, "fat")
	q.Carbs = parseRange(v, "carbs_min", "carbs_max", problems, "carbs")

	q.IncludeIngredients = normalizeTerms(
		splitList(v, "include", "include[]"), MaxIngredientTerms, problems, "include")
	q.ExcludeIngredients = normalizeTerms(
		splitList(v, "exclude", "exclude[]"), MaxIngredientTerms, problems, "exclude")
	q.Pantry = normalizeTerms(
		splitList(v, "pantry", "pantry[]"), MaxPantryTerms, problems, "pantry")

	if raw := v.Get("can_cook_now"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			problems["can_cook_now"] = "Ожидается true или false"
		} else {
			q.CanCookNow = b
		}
	}
	// "Can cook now" with an empty pantry would return nothing but look like a
	// bug to the user, so it is rejected explicitly.
	if q.CanCookNow && len(q.Pantry) == 0 {
		problems["pantry"] = "Для фильтра «могу приготовить сейчас» нужен список продуктов"
	}

	if raw := v.Get("access"); raw != "" {
		switch AccessFilter(raw) {
		case AccessAny, AccessFree, AccessPaid:
			q.Access = AccessFilter(raw)
		default:
			problems["access"] = "Допустимые значения: any, free, paid"
		}
	}

	if raw := v.Get("has_video"); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			problems["has_video"] = "Ожидается true или false"
		} else {
			q.HasVideo = &b
		}
	}

	q.AuthorHandle = strings.ToLower(strings.TrimSpace(v.Get("author")))
	if q.AuthorHandle != "" && !isHandle(q.AuthorHandle) {
		problems["author"] = "Некорректный псевдоним автора"
	}

	q.CollectionID = strings.TrimSpace(v.Get("collection"))

	if raw := v.Get("sort"); raw != "" {
		switch SortMode(raw) {
		case SortRelevance, SortNew, SortPopular, SortRating, SortQuick, SortLight:
			q.Sort = SortMode(raw)
		default:
			problems["sort"] = "Допустимые значения: relevance, new, popular, rating, quick, light"
		}
	} else if q.Text != "" {
		// A text search without an explicit order should rank by relevance.
		q.Sort = SortRelevance
	}
	// Relevance is undefined without a query; fall back rather than returning
	// an arbitrary order the client cannot reproduce.
	if q.Sort == SortRelevance && q.Text == "" {
		q.Sort = SortNew
	}

	if raw := v.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		switch {
		case err != nil || n < 1:
			problems["limit"] = "Ожидается число больше нуля"
		case n > MaxLimit:
			q.Limit = MaxLimit
		default:
			q.Limit = n
		}
	}

	if raw := v.Get("cursor"); raw != "" {
		c, err := DecodeCursor(raw)
		if err != nil {
			problems["cursor"] = "Некорректный курсор постраничной навигации"
		} else if c.Sort != q.Sort {
			// Continuing a cursor under a different ordering would silently skip
			// or repeat rows.
			problems["cursor"] = "Курсор относится к другой сортировке"
		} else {
			q.Cursor = c
		}
	}

	if len(problems) > 0 {
		return Query{}, httpx.Validation(problems)
	}
	return q, nil
}

func splitList(v url.Values, keys ...string) []string {
	var out []string
	for _, k := range keys {
		for _, raw := range v[k] {
			for _, part := range strings.Split(raw, ",") {
				if part = strings.TrimSpace(part); part != "" {
					out = append(out, part)
				}
			}
		}
	}
	return out
}

func parseSlugList(v url.Values, key, altKey string, problems map[string]string, field string) []string {
	items := splitList(v, key, altKey)
	if len(items) > MaxFacetTerms {
		problems[field] = fmt.Sprintf("Не более %d значений", MaxFacetTerms)
		return nil
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, it := range items {
		s := strings.ToLower(it)
		if !isSlug(s) {
			problems[field] = "Допустимы латинские буквы, цифры, дефис и подчёркивание"
			return nil
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func normalizeTerms(items []string, max int, problems map[string]string, field string) []string {
	if len(items) > max {
		problems[field] = fmt.Sprintf("Не более %d значений", max)
		return nil
	}
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, it := range items {
		key := NormalizeKey(it)
		if key == "" {
			continue
		}
		if len([]rune(key)) > 120 {
			problems[field] = "Слишком длинное название продукта"
			return nil
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func parseRange(v url.Values, minKey, maxKey string, problems map[string]string, field string) Range {
	var r Range
	if raw := v.Get(minKey); raw != "" {
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f < 0 {
			problems[field] = "Ожидается неотрицательное число"
			return Range{}
		}
		r.Min = &f
	}
	if raw := v.Get(maxKey); raw != "" {
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f < 0 {
			problems[field] = "Ожидается неотрицательное число"
			return Range{}
		}
		r.Max = &f
	}
	if r.Min != nil && r.Max != nil && *r.Min > *r.Max {
		problems[field] = "Минимум больше максимума"
		return Range{}
	}
	return r
}

func isSlug(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func isHandle(s string) bool {
	if len(s) < 3 || len(s) > 30 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
			return false
		}
	}
	return true
}
