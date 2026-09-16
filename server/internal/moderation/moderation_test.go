package moderation

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
	t.Cleanup(pool.Close)
	return pool
}

// scenario creates an isolated author, moderator and draft recipe, and removes
// them afterwards, so these tests can run against a database holding other data.
type scenario struct {
	authorID    string
	moderatorID string
	recipeID    string
}

func newScenario(t *testing.T, pool *pgxpool.Pool) scenario {
	t.Helper()
	// Passed as text: Postgres cannot infer a type for an integer
	// placeholder used in string concatenation.
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)

	var s scenario
	mustScan(t, pool, &s.authorID, `
		INSERT INTO users (handle, display_name, is_author)
		VALUES ('mod_author_'||$1, 'Автор', true) RETURNING id::text`, suffix)
	mustScan(t, pool, &s.moderatorID, `
		INSERT INTO users (handle, display_name, is_admin)
		VALUES ('mod_admin_'||$1, 'Модератор', true) RETURNING id::text`, suffix)
	mustScan(t, pool, &s.recipeID, `
		INSERT INTO recipes (author_id, slug, title, status, base_servings)
		VALUES ($1::uuid, 'mod-recipe-'||$2, 'На проверку', 'draft', 2)
		RETURNING id::text`, s.authorID, suffix)

	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY(ARRAY[$1,$2]::uuid[])`,
			s.authorID, s.moderatorID)
	})
	return s
}

func mustScan(t *testing.T, pool *pgxpool.Pool, dst *string, sql string, args ...any) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(dst); err != nil {
		t.Fatalf("setup query failed: %v", err)
	}
}

func statusOf(t *testing.T, pool *pgxpool.Pool, recipeID string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM recipes WHERE id=$1::uuid`, recipeID).Scan(&s); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return s
}

// TestPublishWorkflow walks the whole path a recipe takes to the storefront.
func TestPublishWorkflow(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	s := newScenario(t, pool)
	ctx := context.Background()

	if err := repo.Submit(ctx, s.recipeID, s.authorID); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got := statusOf(t, pool, s.recipeID); got != "review" {
		t.Fatalf("after submit: got %q, want review", got)
	}

	// Rejection returns it to the author with a reason.
	if err := repo.Reject(ctx, s.recipeID, s.moderatorID, "Нужно фото готового блюда"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if got := statusOf(t, pool, s.recipeID); got != "rejected" {
		t.Fatalf("after reject: got %q, want rejected", got)
	}

	// A rejected recipe can be resubmitted and then approved.
	if err := repo.Submit(ctx, s.recipeID, s.authorID); err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if err := repo.Approve(ctx, s.recipeID, s.moderatorID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got := statusOf(t, pool, s.recipeID); got != "published" {
		t.Fatalf("after approve: got %q, want published", got)
	}

	var firstPublished time.Time
	if err := pool.QueryRow(ctx,
		`SELECT published_at FROM recipes WHERE id=$1::uuid`, s.recipeID).Scan(&firstPublished); err != nil {
		t.Fatalf("read published_at: %v", err)
	}

	// Re-approving after an edit must NOT move published_at, or a typo fix
	// would relaunch the recipe to the top of the "new" shelf.
	if _, err := pool.Exec(ctx,
		`UPDATE recipes SET status='review' WHERE id=$1::uuid`, s.recipeID); err != nil {
		t.Fatalf("back to review: %v", err)
	}
	if err := repo.Approve(ctx, s.recipeID, s.moderatorID); err != nil {
		t.Fatalf("re-approve: %v", err)
	}
	var secondPublished time.Time
	if err := pool.QueryRow(ctx,
		`SELECT published_at FROM recipes WHERE id=$1::uuid`, s.recipeID).Scan(&secondPublished); err != nil {
		t.Fatalf("read published_at again: %v", err)
	}
	if !firstPublished.Equal(secondPublished) {
		t.Errorf("published_at moved on re-approval: %v -> %v", firstPublished, secondPublished)
	}
}

// TestSubmitRejectsForeignRecipe proves one author cannot push another's draft.
func TestSubmitRejectsForeignRecipe(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	s := newScenario(t, pool)

	err := repo.Submit(context.Background(), s.recipeID, s.moderatorID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("submitting someone else's recipe: got %v, want ErrNotFound", err)
	}
}

// TestApproveRequiresReview checks the state machine refuses to skip review.
func TestApproveRequiresReview(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	s := newScenario(t, pool)

	err := repo.Approve(context.Background(), s.recipeID, s.moderatorID)
	if !errors.Is(err, ErrBadState) {
		t.Errorf("approving a draft: got %v, want ErrBadState", err)
	}
}

// TestReportDeduplication covers the queue-flooding guard.
func TestReportDeduplication(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	s := newScenario(t, pool)
	ctx := context.Background()

	in := ReportInput{TargetType: "recipe", TargetID: s.recipeID, Reason: "spam"}
	if _, err := repo.Report(ctx, s.moderatorID, in); err != nil {
		t.Fatalf("first report: %v", err)
	}
	if _, err := repo.Report(ctx, s.moderatorID, in); !errors.Is(err, ErrDuplicate) {
		t.Errorf("second report: got %v, want ErrDuplicate", err)
	}
}

// TestTakedownResolvesReports checks a taken-down recipe does not linger in the
// moderation queue.
func TestTakedownResolvesReports(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	s := newScenario(t, pool)
	ctx := context.Background()

	if err := repo.Submit(ctx, s.recipeID, s.authorID); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := repo.Approve(ctx, s.recipeID, s.moderatorID); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := repo.Report(ctx, s.authorID,
		ReportInput{TargetType: "recipe", TargetID: s.recipeID, Reason: "unsafe_food"}); err != nil {
		t.Fatalf("Report: %v", err)
	}

	if err := repo.Takedown(ctx, s.recipeID, s.moderatorID, "Небезопасная обработка мяса"); err != nil {
		t.Fatalf("Takedown: %v", err)
	}
	if got := statusOf(t, pool, s.recipeID); got != "archived" {
		t.Errorf("after takedown: got %q, want archived", got)
	}
	var open int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM reports
		 WHERE target_type='recipe' AND target_id=$1::uuid AND status IN ('open','reviewing')`,
		s.recipeID).Scan(&open); err != nil {
		t.Fatalf("count reports: %v", err)
	}
	if open != 0 {
		t.Errorf("takedown left %d open reports behind", open)
	}
}

// TestSuspendRevokesSessions checks suspension cuts access immediately rather
// than waiting for tokens to expire.
func TestSuspendRevokesSessions(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	s := newScenario(t, pool)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1::uuid, $2, now() + interval '30 days')`,
		s.authorID, []byte("test-token-hash-not-a-real-token")); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	if err := repo.Suspend(ctx, s.authorID, s.moderatorID, "Спам"); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	var live int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM refresh_tokens WHERE user_id=$1::uuid AND revoked_at IS NULL`,
		s.authorID).Scan(&live); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if live != 0 {
		t.Errorf("suspension left %d live sessions", live)
	}
	if err := repo.Suspend(ctx, s.authorID, s.moderatorID, "again"); !errors.Is(err, ErrBadState) {
		t.Errorf("suspending twice: got %v, want ErrBadState", err)
	}
}

// TestBlockIsIdempotent covers the viewer-side block used by the storefront.
func TestBlockIsIdempotent(t *testing.T) {
	pool := testPool(t)
	repo := NewRepo(pool)
	s := newScenario(t, pool)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := repo.Block(ctx, s.moderatorID, s.authorID); err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
	}
	if err := repo.Unblock(ctx, s.moderatorID, s.authorID); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_blocks WHERE blocker_id=$1::uuid`, s.moderatorID).Scan(&n); err != nil {
		t.Fatalf("count blocks: %v", err)
	}
	if n != 0 {
		t.Errorf("unblock left %d rows", n)
	}
}
