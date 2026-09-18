package ranking

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/testdb"
)

type stubResolver struct{}

func (stubResolver) PublicURL(key string) string { return "https://cdn.test/" + key }

type rig struct {
	pool *pgxpool.Pool
	repo *Repo
}

func newRig(t *testing.T) *rig {
	t.Helper()
	pool := testdb.Pool(t, "ranking")
	testdb.Truncate(t, pool, "users", "recipes", "posts", "recipe_imports",
		"ratings", "follows", "author_ranks", "user_stats", "moderation_events")
	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	return &rig{pool: pool, repo: NewRepo(pool, quiet)}
}

// author creates an account old enough to be eligible, with the given trust.
func (r *rig) author(t *testing.T, handle string, ageDays int, trust int) string {
	t.Helper()
	var id string
	if err := r.pool.QueryRow(context.Background(), `
		INSERT INTO users (handle, display_name, is_author, trust_level, created_at)
		VALUES ($1, $2, true, $3, now() - make_interval(days => $4))
		RETURNING id::text`, handle, handle, int16(trust), int32(ageDays)).Scan(&id); err != nil {
		t.Fatalf("create author: %v", err)
	}
	return id
}

func (r *rig) recipe(t *testing.T, authorID, slug string) string {
	t.Helper()
	var id string
	if err := r.pool.QueryRow(context.Background(), `
		INSERT INTO recipes (author_id, slug, title, status, published_at)
		VALUES ($1::uuid, $2, $3, 'published', now()) RETURNING id::text`,
		authorID, slug, "Рецепт "+slug).Scan(&id); err != nil {
		t.Fatalf("create recipe: %v", err)
	}
	return id
}

// importer creates an account old enough to have its imports counted.
func (r *rig) importer(t *testing.T, handle string, ageDays int) string {
	t.Helper()
	var id string
	if err := r.pool.QueryRow(context.Background(), `
		INSERT INTO users (handle, display_name, created_at)
		VALUES ($1, $2, now() - make_interval(days => $3)) RETURNING id::text`,
		handle, handle, int32(ageDays)).Scan(&id); err != nil {
		t.Fatalf("create importer: %v", err)
	}
	return id
}

func (r *rig) importRecipe(t *testing.T, userID, recipeID string) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(), `
		INSERT INTO recipe_imports (user_id, recipe_id) VALUES ($1::uuid, $2::uuid)`,
		userID, recipeID); err != nil {
		t.Fatalf("import: %v", err)
	}
}

// seedAuthor builds an author who clears every eligibility gate.
func (r *rig) seedAuthor(t *testing.T, handle string, importers int) string {
	t.Helper()
	id := r.author(t, handle, 60, 1)
	var recipe string
	for i := range minPublishedRecipes {
		recipe = r.recipe(t, id, fmt.Sprintf("%s-%d", handle, i))
	}
	for i := range importers {
		u := r.importer(t, fmt.Sprintf("%s_fan_%d", handle, i), 30)
		r.importRecipe(t, u, recipe)
	}
	return id
}

func (r *rig) rankOf(t *testing.T, userID string) int {
	t.Helper()
	b, err := r.repo.RankOf(context.Background(), userID)
	if err != nil {
		t.Fatalf("RankOf: %v", err)
	}
	if b == nil {
		return 0
	}
	return b.Rank
}

func TestRanksByImports(t *testing.T) {
	r := newRig(t)
	popular := r.seedAuthor(t, "popular", 40)
	modest := r.seedAuthor(t, "modest", 12)

	if err := r.repo.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := r.rankOf(t, popular); got != 1 {
		t.Errorf("popular author rank = %d, want 1", got)
	}
	if got := r.rankOf(t, modest); got != 2 {
		t.Errorf("modest author rank = %d, want 2", got)
	}
}

// TestSelfImportsDoNotCount: without this, the cheapest way to the top is to
// import your own recipes.
func TestSelfImportsDoNotCount(t *testing.T) {
	r := newRig(t)
	honest := r.seedAuthor(t, "honest", 12)
	cheat := r.seedAuthor(t, "cheat", 10)

	// The cheat imports their own work thirty times over.
	var own string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT id::text FROM recipes WHERE author_id=$1::uuid LIMIT 1`, cheat).Scan(&own); err != nil {
		t.Fatalf("find recipe: %v", err)
	}
	for range 30 {
		r.importRecipe(t, cheat, own)
	}

	if err := r.repo.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if r.rankOf(t, honest) != 1 {
		t.Errorf("self-imports outranked real readers: honest=%d cheat=%d",
			r.rankOf(t, honest), r.rankOf(t, cheat))
	}
}

// TestThrowawayImportersDoNotCount: distinct-user counting alone does not stop
// someone registering accounts to import their own work, so an importer has to
// predate the campaign.
func TestThrowawayImportersDoNotCount(t *testing.T) {
	r := newRig(t)
	honest := r.seedAuthor(t, "honest", 12)
	cheat := r.seedAuthor(t, "cheat", 2)

	var own string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT id::text FROM recipes WHERE author_id=$1::uuid LIMIT 1`, cheat).Scan(&own); err != nil {
		t.Fatalf("find recipe: %v", err)
	}
	// Thirty accounts registered yesterday.
	for i := range 30 {
		u := r.importer(t, fmt.Sprintf("sock_%d", i), 1)
		r.importRecipe(t, u, own)
	}

	if err := r.repo.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if r.rankOf(t, honest) != 1 {
		t.Errorf("a farm of fresh accounts outranked real readers: honest=%d cheat=%d",
			r.rankOf(t, honest), r.rankOf(t, cheat))
	}
}

// TestThinSampleIsPulledToTheMean is what the Bayesian prior actually buys.
//
// It does NOT make forty four-star reviews outrank one five-star: 4.0 really is
// below 5.0, and no amount of shrinkage should invert that. What it prevents is
// a single review *dominating* -- a thin sample is pulled towards the store-wide
// average, so one perfect score cannot beat a long record that is genuinely
// better than average.
func TestThinSampleIsPulledToTheMean(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()

	established := r.seedAuthor(t, "established", 20)
	newcomer := r.seedAuthor(t, "newcomer", 20)
	// A crowd of mediocre authors sets a global mean well below the
	// established author's average.
	crowd := r.seedAuthor(t, "crowd", 20)

	rate := func(authorID, tag string, scores ...int) {
		var recipe string
		if err := r.pool.QueryRow(ctx,
			`SELECT id::text FROM recipes WHERE author_id=$1::uuid LIMIT 1`, authorID).Scan(&recipe); err != nil {
			t.Fatalf("find recipe: %v", err)
		}
		for i, sc := range scores {
			u := r.importer(t, fmt.Sprintf("rater_%s_%d", tag, i), 30)
			if _, err := r.pool.Exec(ctx,
				`INSERT INTO ratings (user_id, recipe_id, score) VALUES ($1::uuid, $2::uuid, $3)`,
				u, recipe, int16(sc)); err != nil {
				t.Fatalf("rate: %v", err)
			}
		}
	}
	rep := func(n, score int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = score
		}
		return out
	}
	rate(established, "est", rep(40, 5)...)
	rate(crowd, "crowd", rep(60, 2)...)
	rate(newcomer, "new", 5)

	if err := r.repo.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	score := func(id string) float64 {
		var s float64
		if err := r.pool.QueryRow(ctx,
			`SELECT score FROM author_ranks WHERE user_id=$1::uuid`, id).Scan(&s); err != nil {
			t.Fatalf("read score: %v", err)
		}
		return s
	}
	if score(newcomer) >= score(established) {
		t.Errorf("a single five-star review (%.4f) matched forty of them (%.4f); "+
			"the prior is not shrinking thin samples", score(newcomer), score(established))
	}
}

func TestEligibilityGates(t *testing.T) {
	r := newRig(t)
	eligible := r.seedAuthor(t, "eligible", 20)

	// Too new an account.
	young := r.author(t, "young", 3, 1)
	for i := range minPublishedRecipes {
		rec := r.recipe(t, young, fmt.Sprintf("young-%d", i))
		for j := range 20 {
			r.importRecipe(t, r.importer(t, fmt.Sprintf("y%d_%d", i, j), 30), rec)
		}
	}
	// Not enough imports.
	quiet := r.seedAuthor(t, "quiet", 2)
	// Still premoderated.
	untrusted := r.author(t, "untrusted", 60, 0)
	for i := range minPublishedRecipes {
		rec := r.recipe(t, untrusted, fmt.Sprintf("untrusted-%d", i))
		for j := range 20 {
			r.importRecipe(t, r.importer(t, fmt.Sprintf("u%d_%d", i, j), 30), rec)
		}
	}

	if err := r.repo.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if r.rankOf(t, eligible) == 0 {
		t.Error("an eligible author is missing from the leaderboard")
	}
	for name, id := range map[string]string{
		"account under a month old": young,
		"too few imports":           quiet,
		"still premoderated":        untrusted,
	} {
		if rank := r.rankOf(t, id); rank != 0 {
			t.Errorf("%s was ranked (%d) despite failing its gate", name, rank)
		}
	}
}

// TestTakedownDisqualifies: an upheld complaint in the window is the only
// mitigation there is against stolen content, and it outranks any weighting.
func TestTakedownDisqualifies(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	author := r.seedAuthor(t, "prolific", 40)
	other := r.seedAuthor(t, "clean", 12)

	if err := r.repo.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if r.rankOf(t, author) != 1 {
		t.Fatalf("setup: author should start at rank 1")
	}

	if _, err := r.pool.Exec(ctx, `
		INSERT INTO moderation_events (target_type, target_id, user_id, action)
		VALUES ('recipe', gen_random_uuid(), $1::uuid, 'takedown')`, author); err != nil {
		t.Fatalf("takedown event: %v", err)
	}
	if err := r.repo.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if rank := r.rankOf(t, author); rank != 0 {
		t.Errorf("an author with an upheld takedown still holds rank %d", rank)
	}
	if rank := r.rankOf(t, other); rank != 1 {
		t.Errorf("the remaining author rank = %d, want 1", rank)
	}
	entries, err := r.repo.Leaderboard(ctx, 10, stubResolver{})
	if err != nil {
		t.Fatalf("Leaderboard: %v", err)
	}
	for _, e := range entries {
		if e.UserID == author {
			t.Error("a disqualified author is still on the public leaderboard")
		}
	}
}

func TestPrevRankRecordsMovement(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	first := r.seedAuthor(t, "first", 40)
	second := r.seedAuthor(t, "second", 12)

	if err := r.repo.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// The runner-up gains a crowd and overtakes.
	var recipe string
	if err := r.pool.QueryRow(ctx,
		`SELECT id::text FROM recipes WHERE author_id=$1::uuid LIMIT 1`, second).Scan(&recipe); err != nil {
		t.Fatalf("find recipe: %v", err)
	}
	for i := range 60 {
		r.importRecipe(t, r.importer(t, fmt.Sprintf("late_%d", i), 30), recipe)
	}
	if err := r.repo.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	b, err := r.repo.RankOf(ctx, second)
	if err != nil || b == nil {
		t.Fatalf("RankOf: %v %v", b, err)
	}
	if b.Rank != 1 || b.PrevRank != 2 {
		t.Errorf("rank/prev = %d/%d, want 1/2", b.Rank, b.PrevRank)
	}
	_ = first
}

func TestTopAuthorsReturnsBadges(t *testing.T) {
	r := newRig(t)
	for i := range 5 {
		r.seedAuthor(t, fmt.Sprintf("author%d", i), 10+i*5)
	}
	if err := r.repo.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	top, err := r.repo.TopAuthors(context.Background(), BadgeTop)
	if err != nil {
		t.Fatalf("TopAuthors: %v", err)
	}
	if len(top) != BadgeTop {
		t.Errorf("got %d badges, want %d", len(top), BadgeTop)
	}
	for _, b := range top {
		if b.Rank < 1 || b.Rank > BadgeTop {
			t.Errorf("badge rank %d outside the top %d", b.Rank, BadgeTop)
		}
	}
}

// TestShortsFeedScoreIsFrozenAndOrdered: the feed score exists so a reader can
// page a ranked feed without rows moving under them.
func TestShortsFeedScoreIsFrozenAndOrdered(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	author := r.author(t, "shorty", 60, 1)

	mk := func(title string, likes int, ageDays int) string {
		var media, id string
		if err := r.pool.QueryRow(ctx, `
			INSERT INTO media_assets (owner_id, kind, status, storage_key)
			VALUES ($1::uuid, 'video', 'ready', 'orig/v.mp4') RETURNING id::text`,
			author).Scan(&media); err != nil {
			t.Fatalf("media: %v", err)
		}
		if err := r.pool.QueryRow(ctx, `
			INSERT INTO posts (author_id, kind, title, video_media_id, video_ready,
			                   status, published_at, like_count)
			VALUES ($1::uuid, 'short', $2, $3::uuid, true, 'published',
			        now() - make_interval(days => $4), $5)
			RETURNING id::text`, author, title, media, int32(ageDays), int64(likes)).Scan(&id); err != nil {
			t.Fatalf("short: %v", err)
		}
		return id
	}
	hit := mk("Популярный", 500, 1)
	old := mk("Старый хит", 500, 60)
	fresh := mk("Свежий тихий", 1, 0)

	if err := r.repo.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	score := func(id string) float64 {
		var s float64
		if err := r.pool.QueryRow(ctx, `SELECT feed_score FROM posts WHERE id=$1::uuid`, id).Scan(&s); err != nil {
			t.Fatalf("read score: %v", err)
		}
		return s
	}
	if score(hit) <= score(old) {
		t.Errorf("a two-month-old hit (%.4f) ranks at or above today's (%.4f); recency is not decaying",
			score(old), score(hit))
	}
	if score(fresh) <= 0 {
		t.Error("a brand new short scored zero and would never surface")
	}

	// The epoch moves with each refresh so a cursor from before can be spotted.
	var e1, e2 int64
	if err := r.pool.QueryRow(ctx, `SELECT epoch FROM ranking_state WHERE id`).Scan(&e1); err != nil {
		t.Fatalf("epoch: %v", err)
	}
	if err := r.repo.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if err := r.pool.QueryRow(ctx, `SELECT epoch FROM ranking_state WHERE id`).Scan(&e2); err != nil {
		t.Fatalf("epoch: %v", err)
	}
	if e2 <= e1 {
		t.Errorf("epoch did not advance: %d -> %d", e1, e2)
	}
}

// TestRefreshReconcilesCounters: follower and import counts are deliberately
// not trigger-maintained, so this job is what makes them true.
func TestRefreshReconcilesCounters(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	author := r.seedAuthor(t, "counted", 12)
	fan := r.importer(t, "a_fan", 30)

	// Insert a follow with the trigger disabled, simulating drift.
	if _, err := r.pool.Exec(ctx, `ALTER TABLE follows DISABLE TRIGGER follows_apply`); err != nil {
		t.Fatalf("disable trigger: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO follows (follower_id, author_id) VALUES ($1::uuid, $2::uuid)`,
		fan, author); err != nil {
		t.Fatalf("follow: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `ALTER TABLE follows ENABLE TRIGGER follows_apply`); err != nil {
		t.Fatalf("enable trigger: %v", err)
	}

	var before int
	if err := r.pool.QueryRow(ctx,
		`SELECT follower_count FROM user_stats WHERE user_id=$1::uuid`, author).Scan(&before); err != nil {
		t.Fatalf("read: %v", err)
	}
	if before != 0 {
		t.Fatalf("setup: expected drift, got follower_count = %d", before)
	}

	if err := r.repo.Refresh(ctx); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	var after int
	if err := r.pool.QueryRow(ctx,
		`SELECT follower_count FROM user_stats WHERE user_id=$1::uuid`, author).Scan(&after); err != nil {
		t.Fatalf("read: %v", err)
	}
	if after != 1 {
		t.Errorf("follower_count after reconcile = %d, want 1", after)
	}
}
