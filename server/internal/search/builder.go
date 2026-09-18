package search

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/paging"
)

// argset accumulates bind parameters. Every dynamic value in a query goes
// through next(), so no caller-supplied string is ever concatenated into SQL.
type argset struct {
	vals []any
}

func (a *argset) next(v any) string {
	a.vals = append(a.vals, v)
	return "$" + strconv.Itoa(len(a.vals))
}

// selectColumns is the storefront card projection. It is deliberately narrow:
// descriptions and step bodies belong to the detail endpoint, not the feed.
const selectColumns = `
        r.id, r.slug, r.title, coalesce(r.summary, '') AS summary,
        r.cook_time_minutes, r.difficulty, r.base_servings,
        r.category_id, r.cuisine_id,
        r.access_tier, r.price_minor, r.currency,
        r.kcal_per_serving, r.protein_per_serving, r.fat_per_serving, r.carbs_per_serving,
        r.has_video, r.import_count, r.favorite_count,
        r.rating_avg, r.rating_count, r.published_at, r.diet_slugs,
        u.id AS author_id, u.handle AS author_handle, u.display_name AS author_name,
        u.verified_at IS NOT NULL AS author_verified, av.storage_key AS author_avatar_key,
        hero.media_id, hero.kind AS media_kind, hero.poster_key, hero.hls_key,
        hero.storage_key, hero.blurhash, hero.width, hero.height`

// avatarJoin resolves the author's avatar. It is a plain left join on a
// primary key rather than another LATERAL: there is at most one avatar per
// author and the row is almost always already in cache.
const avatarJoin = `
    LEFT JOIN media_assets av ON av.id = u.avatar_media_id AND av.status = 'ready'`

// heroJoin picks one display asset per card, preferring an explicit hero and
// then the author's ordering. LATERAL ... LIMIT 1 keeps this at one index hit
// per row instead of fanning out and de-duplicating afterwards.
const heroJoin = `
    LEFT JOIN LATERAL (
        SELECT m.id AS media_id, m.kind, m.poster_key, m.hls_key,
               m.storage_key, m.blurhash, m.width, m.height
          FROM recipe_media rm
          JOIN media_assets m ON m.id = rm.media_id
         WHERE rm.recipe_id = r.id AND m.status = 'ready'
         ORDER BY (rm.role = 'hero') DESC, rm.position ASC, m.id ASC
         LIMIT 1
    ) hero ON true`

// buildBase renders the filtered projection shared by the list and facet
// queries. Keeping one definition of "what matches" means a facet count can
// never disagree with the list it labels.
//
// viewerID may be empty for anonymous callers; when present, recipes by authors
// the viewer has blocked are removed, which App Store Guideline 1.2 expects a
// UGC app to honour.
func buildBase(q Query, viewerID string, a *argset) string {
	var where []string

	// Matches the partial indexes exactly; every read-path index is declared
	// WHERE status='published' AND deleted_at IS NULL.
	where = append(where, "r.status = 'published'", "r.deleted_at IS NULL")
	// A suspended author's catalogue disappears from the storefront without
	// having to rewrite each of their recipes.
	where = append(where, "u.status = 'active'")

	rank := "0::real"
	if q.Text != "" {
		tsq := a.next(q.Text)
		trg := a.next(q.Text)
		// websearch_to_tsquery never raises on user input, unlike to_tsquery.
		// The trigram arm catches typos and short fragments that stemming misses.
		where = append(where, fmt.Sprintf(
			"(r.search_vector @@ websearch_to_tsquery('russian', %s) OR r.title %% %s)", tsq, trg))
		rank = fmt.Sprintf(
			"(ts_rank_cd(r.search_vector, websearch_to_tsquery('russian', %s)) + similarity(r.title, %s))",
			tsq, trg)
	}

	if len(q.CategorySlugs) > 0 {
		where = append(where, fmt.Sprintf(
			"r.category_id IN (SELECT id FROM categories WHERE slug = ANY(%s))",
			a.next(q.CategorySlugs)))
	}
	if len(q.CuisineSlugs) > 0 {
		where = append(where, fmt.Sprintf(
			"r.cuisine_id IN (SELECT id FROM cuisines WHERE slug = ANY(%s))",
			a.next(q.CuisineSlugs)))
	}
	if len(q.Difficulties) > 0 {
		where = append(where, fmt.Sprintf("r.difficulty = ANY(%s)", a.next(q.Difficulties)))
	}
	if len(q.DietSlugs) > 0 {
		// @> is "contains all", i.e. AND semantics, answered by the GIN index.
		where = append(where, fmt.Sprintf("r.diet_slugs @> %s", a.next(q.DietSlugs)))
	}

	where = appendRange(where, a, "r.cook_time_minutes", q.CookTime)
	where = appendRange(where, a, "r.kcal_per_serving", q.Kcal)
	where = appendRange(where, a, "r.protein_per_serving", q.Protein)
	where = appendRange(where, a, "r.fat_per_serving", q.Fat)
	where = appendRange(where, a, "r.carbs_per_serving", q.Carbs)

	if len(q.IncludeIngredients) > 0 {
		where = append(where, fmt.Sprintf("r.ingredient_keys @> %s", a.next(q.IncludeIngredients)))
	}
	if len(q.ExcludeIngredients) > 0 {
		// && is "overlaps"; negating it removes anything carrying a listed key.
		// Checked against ingredient_keys (not required_*) so an allergen in an
		// optional ingredient is still caught.
		where = append(where, fmt.Sprintf("NOT (r.ingredient_keys && %s)", a.next(q.ExcludeIngredients)))
	}

	// "Can cook now" is relational division: keep recipes whose every required
	// ingredient is in the pantry. It is expressed as a counting join driven
	// from the pantry side rather than `required_ingredient_keys <@ :pantry`,
	// because GIN cannot drive "contained by" and measured 275 ms against 48 ms
	// for this form on 50k recipes. See migrations/0006 and docs/PERFORMANCE.md.
	//
	// The ::text[] cast is required: a bare placeholder arrives as type
	// "unknown" and = ANY() cannot resolve it.
	pantryJoin := ""
	if q.CanCookNow && len(q.Pantry) > 0 {
		pantryJoin = fmt.Sprintf(`
    JOIN (
        SELECT ri.recipe_id, count(*) AS matched
          FROM recipe_ingredients ri
         WHERE NOT ri.is_optional AND ri.product_key = ANY(%s::text[])
         GROUP BY ri.recipe_id
    ) pantry ON pantry.recipe_id = r.id AND pantry.matched = r.required_count`,
			a.next(q.Pantry))
	}

	switch q.Access {
	case AccessFree, AccessPaid:
		where = append(where, fmt.Sprintf("r.access_tier = %s", a.next(string(q.Access))))
	}
	if q.HasVideo != nil {
		where = append(where, fmt.Sprintf("r.has_video = %s", a.next(*q.HasVideo)))
	}
	// Filtering by id rather than handle is what lets the planner drive the
	// whole author page off recipes_author_feed_idx: the index leads with
	// author_id, and a predicate on the joined users table gives it no equality
	// to anchor on. Handlers resolve the handle once, up front.
	if q.AuthorID != "" {
		where = append(where, fmt.Sprintf("r.author_id = %s::uuid", a.next(q.AuthorID)))
	} else if q.AuthorHandle != "" {
		where = append(where, fmt.Sprintf("u.handle = %s", a.next(q.AuthorHandle)))
	}
	if viewerID != "" {
		where = append(where, fmt.Sprintf(
			"NOT EXISTS (SELECT 1 FROM user_blocks b WHERE b.blocker_id = %s AND b.blocked_id = r.author_id)",
			a.next(viewerID)))
	}
	if q.Sort == SortRating {
		// Keeps rating_avg non-NULL so the cursor comparison stays total, and
		// matches the partial index which is declared WHERE rating_count > 0.
		where = append(where, "r.rating_count > 0")
	}

	joins := pantryJoin + heroJoin
	if q.CollectionID != "" {
		joins = fmt.Sprintf(
			"\n    JOIN collection_items ci ON ci.recipe_id = r.id AND ci.collection_id = %s",
			a.next(q.CollectionID)) + joins
	}

	return fmt.Sprintf(`
    SELECT %s,
           %s AS rank
      FROM recipes r
      JOIN users u ON u.id = r.author_id%s%s
     WHERE %s`, selectColumns, rank, avatarJoin, joins, strings.Join(where, "\n       AND "))
}

// buildFollowed renders the "new from authors you follow" shelf.
//
// It drives from follows and takes only the newest few per author, rather than
// filtering the recipe scan with an EXISTS. The difference is not small: on
// 50k recipes an EXISTS filter reads every recipe by every followed author and
// sorts the lot -- 12ms at 50 follows, 18ms at 800. Capping per author first
// leaves a few thousand rows to sort instead: 0.6ms and 4.1ms.
//
// It also makes a better shelf. Without the cap one prolific author fills it
// and everyone else the reader follows is invisible.
func buildFollowed(viewerID string, perAuthor, limit int) (string, []any) {
	a := &argset{}
	viewer := a.next(viewerID)
	per := a.next(perAuthor)
	lim := a.next(limit)

	return fmt.Sprintf(`
    SELECT %s, 0::real AS rank
      FROM follows fl
      JOIN users u ON u.id = fl.author_id AND u.status = 'active'
      JOIN LATERAL (
          SELECT * FROM recipes r2
           WHERE r2.author_id = fl.author_id
             AND r2.status = 'published' AND r2.deleted_at IS NULL
           ORDER BY r2.published_at DESC, r2.id DESC
           LIMIT %s
      ) r ON true%s
     WHERE fl.follower_id = %s::uuid
       AND NOT EXISTS (SELECT 1 FROM user_blocks b
                        WHERE b.blocker_id = %s::uuid AND b.blocked_id = r.author_id)
     ORDER BY r.published_at DESC, r.id DESC
     LIMIT %s`, selectColumns, per, avatarJoin+heroJoin, viewer, viewer, lim), a.vals
}

// buildList renders one page of storefront cards plus its arguments.
func buildList(q Query, viewerID string) (string, []any) {
	a := &argset{}
	inner := buildBase(q, viewerID, a)

	// The keyset predicate is applied outside the projection because the
	// relevance ordering compares a computed rank, which cannot be referenced
	// from the inner WHERE clause.
	outerWhere := "TRUE"
	if q.Cursor != nil {
		outerWhere = keysetPredicate(q.Sort, q.Cursor, a)
	}

	// One extra row is fetched purely to learn whether another page exists,
	// which avoids a second COUNT over the same predicate.
	return fmt.Sprintf(`WITH base AS (%s
)
SELECT * FROM base
 WHERE %s
 ORDER BY %s
 LIMIT %s`, inner, outerWhere, orderBy(q.Sort), a.next(q.Limit+1)), a.vals
}

// buildFacets counts the current result set broken down by each facet, so the
// client can grey out filters that would return nothing. Cursor and ordering
// are irrelevant to a count and are deliberately not applied.
func buildFacets(q Query, viewerID string) (string, []any) {
	a := &argset{}
	inner := buildBase(q, viewerID, a)
	return fmt.Sprintf(`WITH base AS (%s
)
SELECT 'total'::text AS kind, ''::text AS key, count(*)::bigint AS n FROM base
UNION ALL
SELECT 'access', access_tier, count(*)::bigint FROM base GROUP BY access_tier
UNION ALL
SELECT 'category', c.slug, count(*)::bigint
  FROM base b JOIN categories c ON c.id = b.category_id GROUP BY c.slug
UNION ALL
SELECT 'cuisine', cu.slug, count(*)::bigint
  FROM base b JOIN cuisines cu ON cu.id = b.cuisine_id GROUP BY cu.slug
UNION ALL
SELECT 'diet', d.slug, count(*)::bigint
  FROM base b, unnest(b.diet_slugs) AS d(slug) GROUP BY d.slug
UNION ALL
SELECT 'video', CASE WHEN has_video THEN 'true' ELSE 'false' END, count(*)::bigint
  FROM base GROUP BY has_video`, inner), a.vals
}

// orderBy must keep both sort columns in the same direction so the row-wise
// keyset comparison in keysetPredicate stays consistent with the index.
func orderBy(s SortMode) string {
	switch s {
	case SortRelevance:
		return "rank DESC, id DESC"
	case SortPopular:
		return "import_count DESC, id DESC"
	case SortRating:
		return "rating_avg DESC, id DESC"
	case SortQuick:
		return "cook_time_minutes ASC, id ASC"
	case SortLight:
		return "kcal_per_serving ASC, id ASC"
	default:
		return "published_at DESC, id DESC"
	}
}

func keysetPredicate(s SortMode, c *paging.Cursor, a *argset) string {
	switch s {
	case SortRelevance:
		return fmt.Sprintf("(rank, id) < (%s::real, %s::uuid)",
			a.next(c.Num), a.next(c.ID))
	case SortPopular:
		return fmt.Sprintf("(import_count, id) < (%s::bigint, %s::uuid)",
			a.next(int64(c.Num)), a.next(c.ID))
	case SortRating:
		return fmt.Sprintf("(rating_avg, id) < (%s::numeric, %s::uuid)",
			a.next(c.Num), a.next(c.ID))
	case SortQuick:
		return fmt.Sprintf("(cook_time_minutes, id) > (%s::int, %s::uuid)",
			a.next(int(c.Num)), a.next(c.ID))
	case SortLight:
		return fmt.Sprintf("(kcal_per_serving, id) > (%s::numeric, %s::uuid)",
			a.next(c.Num), a.next(c.ID))
	default:
		return fmt.Sprintf("(published_at, id) < (%s::timestamptz, %s::uuid)",
			a.next(c.Time), a.next(c.ID))
	}
}

func appendRange(where []string, a *argset, column string, r Range) []string {
	if !r.isSet() {
		return where
	}
	if r.Min != nil {
		where = append(where, fmt.Sprintf("%s >= %s", column, a.next(*r.Min)))
	}
	if r.Max != nil {
		where = append(where, fmt.Sprintf("%s <= %s", column, a.next(*r.Max)))
	}
	return where
}

// buildOne fetches a single card by id or slug, reusing the same projection and
// visibility rules as the feed so a detail page can never show something the
// listing would have hidden.
func buildOne(idOrSlug, viewerID string) (string, []any) {
	a := &argset{}
	key := a.next(idOrSlug)

	where := []string{
		"r.status = 'published'",
		"r.deleted_at IS NULL",
		"u.status = 'active'",
		// A uuid cast on a non-uuid string raises, so the id arm is guarded by
		// a shape test rather than relying on the planner's evaluation order.
		fmt.Sprintf("(r.slug = %s OR (%s ~ '^[0-9a-fA-F-]{36}$' AND r.id = %s::uuid))",
			key, key, key),
	}
	if viewerID != "" {
		where = append(where, fmt.Sprintf(
			"NOT EXISTS (SELECT 1 FROM user_blocks b WHERE b.blocker_id = %s AND b.blocked_id = r.author_id)",
			a.next(viewerID)))
	}

	return fmt.Sprintf(`
    SELECT %s,
           0::real AS rank
      FROM recipes r
      JOIN users u ON u.id = r.author_id%s%s
     WHERE %s
     LIMIT 1`, selectColumns, avatarJoin, heroJoin, strings.Join(where, "\n       AND ")), a.vals
}
