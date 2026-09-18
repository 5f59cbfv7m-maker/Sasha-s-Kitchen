package search

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// TestNormalizeKeyMatchesSQL guards the invariant the whole ingredient-matching
// design rests on: the Go normaliser and sk_normalize_key() must agree exactly.
// A divergence does not raise an error anywhere, it just silently stops
// matching pantry items to recipes, so it gets its own test.
func TestNormalizeKeyMatchesSQL(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	inputs := []string{
		"Яйцо", "  Молоко  ", "Зелёный лук", "ЗЕЛЁНЫЙ  ЛУК", "Свёкла",
		"Мисо   паста", "ёжик", "Ёлка", "Chicken Breast", "  ", "Мука\tпшеничная",
		"томат\nчерри", "Сыр «Пармезан»",
	}
	for _, in := range inputs {
		var fromSQL string
		if err := pool.QueryRow(ctx, `SELECT sk_normalize_key($1)`, in).Scan(&fromSQL); err != nil {
			t.Fatalf("sk_normalize_key(%q): %v", in, err)
		}
		if got := NormalizeKey(in); got != fromSQL {
			t.Errorf("normalisation diverged for %q:\n  Go:  %q\n  SQL: %q", in, got, fromSQL)
		}
	}
}

// TestFilters drives every filter through the real query builder and database.
func TestFilters(t *testing.T) {
	pool := testPool(t)
	seedFixtures(t, pool)
	repo := NewRepo(pool, stubResolver{})

	cases := []struct {
		name  string
		query string
		want  []string // expected slugs, order-insensitive
	}{
		{"no filters returns published only", "",
			[]string{"omlet", "borsch", "pasta-carbonara", "salat-ovoshnoy", "tiramisu", "sup-miso", "blini"}},

		{"full-text by title", "q=борщ", []string{"borsch"}},
		{"full-text reaches ingredients", "q=молоко", []string{"omlet", "blini"}},
		{"fuzzy tolerates a typo", "q=тирамису", []string{"tiramisu"}},

		{"category", "category=soup", []string{"borsch", "sup-miso"}},
		{"cuisine", "cuisine=italian", []string{"pasta-carbonara", "tiramisu"}},
		{"difficulty", "difficulty=3", []string{"borsch", "tiramisu"}},

		{"diet tags are ANDed", "diet=vegetarian,gluten_free",
			[]string{"omlet", "salat-ovoshnoy"}},

		{"cook time ceiling", "cook_time_max=15",
			[]string{"omlet", "salat-ovoshnoy", "sup-miso"}},
		{"kcal ceiling", "kcal_max=100", []string{"salat-ovoshnoy", "sup-miso"}},
		{"kcal floor", "kcal_min=300", []string{"pasta-carbonara", "tiramisu"}},

		{"include one ingredient", "include=молоко", []string{"omlet", "blini"}},
		{"include is ANDed", "include=яйцо,молоко", []string{"omlet"}},
		{"ё folds when including", "include=свекла", []string{"borsch"}},

		{"exclude removes matches", "exclude=яйцо",
			[]string{"borsch", "salat-ovoshnoy", "sup-miso", "blini"}},
		// The allergen case: peanuts appear only as an OPTIONAL ingredient of
		// tiramisu, and must still be caught.
		{"exclude catches optional ingredients", "exclude=арахис",
			[]string{"omlet", "borsch", "pasta-carbonara", "salat-ovoshnoy", "sup-miso", "blini"}},

		{"can cook now", "can_cook_now=true&pantry=яйцо,молоко", []string{"omlet"}},
		{"can cook now with a fuller pantry",
			"can_cook_now=true&pantry=яйцо,молоко,мука,огурец,помидор",
			[]string{"omlet", "salat-ovoshnoy", "blini"}},
		{"can cook now ignores optional ingredients",
			"can_cook_now=true&pantry=маскарпоне,яйцо", []string{"tiramisu"}},

		{"free only", "access=free",
			[]string{"omlet", "borsch", "salat-ovoshnoy", "sup-miso", "blini"}},
		{"paid only", "access=paid", []string{"pasta-carbonara", "tiramisu"}},

		{"author", "author=chef_boris", []string{"blini"}},

		{"combined filters", "category=soup&diet=vegetarian&cook_time_max=20",
			[]string{"sup-miso"}},
		{"filters that match nothing", "category=dessert&access=free", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := mustParse(t, tc.query)
			q.Limit = MaxLimit
			page, err := repo.List(context.Background(), q, "")
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			got := slugs(page.Items)
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if len(got) == 0 && len(want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("query %q\n got: %v\nwant: %v", tc.query, got, want)
			}
		})
	}
}

// TestDraftIsNeverVisible proves unpublished work cannot leak into the store.
func TestDraftIsNeverVisible(t *testing.T) {
	pool := testPool(t)
	seedFixtures(t, pool)
	repo := NewRepo(pool, stubResolver{})

	q := mustParse(t, "q=черновик")
	page, err := repo.List(context.Background(), q, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("draft recipe surfaced in the storefront: %s", fmtCards(page.Items))
	}
}

// TestSortOrders checks each ordering end to end.
func TestSortOrders(t *testing.T) {
	pool := testPool(t)
	seedFixtures(t, pool)
	repo := NewRepo(pool, stubResolver{})

	cases := []struct {
		sort string
		want []string
	}{
		{"new", []string{"blini", "sup-miso", "tiramisu", "salat-ovoshnoy", "pasta-carbonara", "borsch", "omlet"}},
		{"popular", []string{"borsch", "omlet", "pasta-carbonara", "sup-miso", "salat-ovoshnoy", "tiramisu", "blini"}},
		{"quick", []string{"salat-ovoshnoy", "omlet", "sup-miso", "pasta-carbonara", "blini", "tiramisu", "borsch"}},
		// kcal/serving: 16.5, 96.8, 159.53, 237.67, 245, 311.93, 686.35
		{"light", []string{"salat-ovoshnoy", "sup-miso", "omlet", "borsch", "blini", "tiramisu", "pasta-carbonara"}},
	}
	for _, tc := range cases {
		t.Run(tc.sort, func(t *testing.T) {
			q := mustParse(t, "sort="+tc.sort)
			q.Limit = MaxLimit
			page, err := repo.List(context.Background(), q, "")
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if got := slugs(page.Items); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("sort=%s\n got: %v\nwant: %v", tc.sort, got, tc.want)
			}
		})
	}
}

// TestKeysetPaginationIsStable walks every sort one page at a time and checks
// that the union of pages equals a single large page — no row skipped, none
// repeated. This is the property OFFSET pagination loses under concurrent
// writes and the reason the feed uses keyset cursors.
func TestKeysetPaginationIsStable(t *testing.T) {
	pool := testPool(t)
	seedFixtures(t, pool)
	repo := NewRepo(pool, stubResolver{})
	ctx := context.Background()

	for _, mode := range []string{"new", "popular", "quick", "light"} {
		t.Run(mode, func(t *testing.T) {
			full := mustParse(t, "sort="+mode)
			full.Limit = MaxLimit
			all, err := repo.List(ctx, full, "")
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			want := slugs(all.Items)

			var got []string
			seen := map[string]bool{}
			cursor := ""
			for page := 0; page < 20; page++ {
				raw := "sort=" + mode + "&limit=2"
				if cursor != "" {
					raw += "&cursor=" + cursor
				}
				q := mustParse(t, raw)
				res, err := repo.List(ctx, q, "")
				if err != nil {
					t.Fatalf("page %d: %v", page, err)
				}
				for _, c := range res.Items {
					if seen[c.Slug] {
						t.Fatalf("slug %q returned on more than one page", c.Slug)
					}
					seen[c.Slug] = true
					got = append(got, c.Slug)
				}
				if !res.HasMore {
					break
				}
				if res.NextCursor == "" {
					t.Fatal("HasMore is true but NextCursor is empty")
				}
				cursor = res.NextCursor
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("paged traversal differs from single page\n got: %v\nwant: %v", got, want)
			}
		})
	}
}

// TestFacetsMatchResults checks that a facet count never disagrees with the
// list it labels — the reason list and facets share one filter builder.
func TestFacetsMatchResults(t *testing.T) {
	pool := testPool(t)
	seedFixtures(t, pool)
	repo := NewRepo(pool, stubResolver{})
	ctx := context.Background()

	q := mustParse(t, "access=free")
	q.Limit = MaxLimit
	page, err := repo.List(ctx, q, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	facets, err := repo.Facets(ctx, q, "")
	if err != nil {
		t.Fatalf("Facets: %v", err)
	}
	if facets.Total != int64(len(page.Items)) {
		t.Errorf("facet total %d != %d listed items", facets.Total, len(page.Items))
	}
	if got := facets.Categories["soup"]; got != 2 {
		t.Errorf("free soups: got %d, want 2", got)
	}
	if got := facets.Diets["vegetarian"]; got != 4 {
		t.Errorf("free vegetarian: got %d, want 4", got)
	}
	if _, paid := facets.Access["paid"]; paid {
		t.Error("access=free must not report a paid bucket")
	}
}

// TestBlockedAuthorIsHidden covers the UGC requirement that blocking an author
// actually removes their content for that viewer.
func TestBlockedAuthorIsHidden(t *testing.T) {
	pool := testPool(t)
	seedFixtures(t, pool)
	repo := NewRepo(pool, stubResolver{})
	ctx := context.Background()

	var viewerID, blockedID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (handle, display_name) VALUES ('viewer_one','Зритель')
		RETURNING id::text`).Scan(&viewerID); err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE handle='chef_boris'`).Scan(&blockedID); err != nil {
		t.Fatalf("find author: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1,$2)`,
		viewerID, blockedID); err != nil {
		t.Fatalf("block: %v", err)
	}

	q := mustParse(t, "")
	q.Limit = MaxLimit

	anon, err := repo.List(ctx, q, "")
	if err != nil {
		t.Fatalf("List anonymous: %v", err)
	}
	if !contains(slugs(anon.Items), "blini") {
		t.Fatalf("fixture problem: blini missing for anonymous viewer: %s", fmtCards(anon.Items))
	}

	blocked, err := repo.List(ctx, q, viewerID)
	if err != nil {
		t.Fatalf("List as blocker: %v", err)
	}
	if contains(slugs(blocked.Items), "blini") {
		t.Errorf("blocked author's recipe still visible: %s", fmtCards(blocked.Items))
	}
}

// TestSuspendedAuthorIsHidden checks that suspending an account takes their
// whole catalogue off the storefront without touching each recipe.
func TestSuspendedAuthorIsHidden(t *testing.T) {
	pool := testPool(t)
	seedFixtures(t, pool)
	repo := NewRepo(pool, stubResolver{})
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`UPDATE users SET status='suspended' WHERE handle='chef_boris'`); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	q := mustParse(t, "")
	q.Limit = MaxLimit
	page, err := repo.List(ctx, q, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if contains(slugs(page.Items), "blini") {
		t.Errorf("suspended author's recipe still visible: %s", fmtCards(page.Items))
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestListFollowedCapsPerAuthor: the shelf exists to show what the people you
// follow have been cooking, so one prolific author must not fill it. The cap is
// also what keeps the query fast -- without it the plan reads every recipe by
// every followed author and sorts the lot.
func TestListFollowedCapsPerAuthor(t *testing.T) {
	pool := testPool(t)
	seedFixtures(t, pool)
	repo := NewRepo(pool, stubResolver{})
	ctx := context.Background()

	var anna, boris, reader string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE handle='chef_anna'`).Scan(&anna); err != nil {
		t.Fatalf("anna: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE handle='chef_boris'`).Scan(&boris); err != nil {
		t.Fatalf("boris: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (handle, display_name) VALUES ('follower_one','Подписчик')
		RETURNING id::text`).Scan(&reader); err != nil {
		t.Fatalf("reader: %v", err)
	}
	for _, author := range []string{anna, boris} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO follows (follower_id, author_id) VALUES ($1::uuid, $2::uuid)`,
			reader, author); err != nil {
			t.Fatalf("follow: %v", err)
		}
	}

	items, err := repo.ListFollowed(ctx, reader, 1, 20)
	if err != nil {
		t.Fatalf("ListFollowed: %v", err)
	}
	perAuthor := map[string]int{}
	for _, c := range items {
		perAuthor[c.Author.Handle]++
	}
	for handle, n := range perAuthor {
		if n > 1 {
			t.Errorf("%s contributed %d recipes despite a cap of 1", handle, n)
		}
	}
	if len(perAuthor) < 2 {
		t.Errorf("shelf covers %d authors, want both followed ones", len(perAuthor))
	}

	// Someone who follows nobody gets an empty shelf, not everyone's recipes.
	var stranger string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (handle, display_name) VALUES ('follows_nobody','Никого')
		RETURNING id::text`).Scan(&stranger); err != nil {
		t.Fatalf("stranger: %v", err)
	}
	empty, err := repo.ListFollowed(ctx, stranger, 3, 20)
	if err != nil {
		t.Fatalf("ListFollowed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("a reader following nobody got %d recipes", len(empty))
	}
}

// TestListFollowedHonoursBlocks: blocking must silence an author everywhere,
// including a shelf built from an older subscription.
func TestListFollowedHonoursBlocks(t *testing.T) {
	pool := testPool(t)
	seedFixtures(t, pool)
	repo := NewRepo(pool, stubResolver{})
	ctx := context.Background()

	var anna, reader string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE handle='chef_anna'`).Scan(&anna); err != nil {
		t.Fatalf("anna: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (handle, display_name) VALUES ('blocker_one','Блокирующий')
		RETURNING id::text`).Scan(&reader); err != nil {
		t.Fatalf("reader: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO follows (follower_id, author_id) VALUES ($1::uuid, $2::uuid)`,
		reader, anna); err != nil {
		t.Fatalf("follow: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1::uuid, $2::uuid)`,
		reader, anna); err != nil {
		t.Fatalf("block: %v", err)
	}

	items, err := repo.ListFollowed(ctx, reader, 3, 20)
	if err != nil {
		t.Fatalf("ListFollowed: %v", err)
	}
	for _, c := range items {
		if c.Author.Handle == "chef_anna" {
			t.Error("a blocked author still appears in the following shelf")
		}
	}
}
