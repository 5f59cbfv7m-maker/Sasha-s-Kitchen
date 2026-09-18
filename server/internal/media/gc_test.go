package media

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/objstore"
)

// gcRig builds a collector over the flow rig's pool and fake object store.
func gcRig(t *testing.T) (*flowRig, *Collector) {
	t.Helper()
	rig := newFlowRig(t)
	// Other tests in this package leave assets behind, and a collector sweeps
	// whatever it finds. Start from an empty table so a count means something.
	//
	// DELETE rather than TRUNCATE CASCADE: cascade follows foreign keys
	// INBOUND, and users.avatar_media_id points at media_assets, so truncating
	// it would take the rig's owner with it.
	for _, table := range []string{"posts", "recipes", "media_assets"} {
		if _, err := rig.pool.Exec(context.Background(), `DELETE FROM `+table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	c := NewCollector(rig.repo, rig.store, quiet)
	// The default grace is a day; these assets are seconds old.
	c.Grace = 0
	return rig, c
}

// asset inserts a ready asset with an object behind it.
func (r *flowRig) asset(t *testing.T, key string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO media_assets (owner_id, kind, status, storage_key)
		VALUES ($1, 'photo', 'ready', $2) RETURNING id`, r.ownerID, key).Scan(&id); err != nil {
		t.Fatalf("create asset: %v", err)
	}
	if err := r.store.Put(ctx, key, bytesReader(make([]byte, 16)), 16, "image/jpeg"); err != nil {
		t.Fatalf("put object: %v", err)
	}
	return id
}

func TestGCDeletesUnreferencedAssets(t *testing.T) {
	rig, c := gcRig(t)
	ctx := context.Background()
	id := rig.asset(t, "orphan/a.jpg")

	n, err := c.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("collected %d assets, want 1", n)
	}
	if rig.has("orphan/a.jpg") {
		t.Error("the object is still in storage")
	}
	var left int
	if err := rig.pool.QueryRow(ctx,
		`SELECT count(*) FROM media_assets WHERE id=$1`, id).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Error("the row survived its object")
	}
}

// TestGCRespectsGracePeriod is the rule that stops the collector eating uploads
// in progress: an asset is created 'pending' and only attached once the client
// has finished, so a sweep with no grace deletes work in flight.
func TestGCRespectsGracePeriod(t *testing.T) {
	rig, c := gcRig(t)
	c.Grace = DefaultGrace
	rig.asset(t, "fresh/a.jpg")

	n, err := c.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 0 {
		t.Errorf("collected %d assets inside the grace period", n)
	}
	if !rig.has("fresh/a.jpg") {
		t.Error("an in-flight upload was deleted")
	}
}

// TestGCKeepsEveryReferencedAsset walks the whole referrer set. A seventh place
// that can hold a media id, added without updating sk_media_unreferenced, would
// show up here as a deleted file that is still on someone's page.
func TestGCKeepsEveryReferencedAsset(t *testing.T) {
	rig, c := gcRig(t)
	ctx := context.Background()

	var recipeID, postID string
	if err := rig.pool.QueryRow(ctx, `
		INSERT INTO recipes (author_id, slug, title, status, published_at)
		VALUES ($1, 'gc-recipe', 'Рецепт', 'published', now()) RETURNING id::text`,
		rig.ownerID).Scan(&recipeID); err != nil {
		t.Fatalf("recipe: %v", err)
	}
	if err := rig.pool.QueryRow(ctx, `
		INSERT INTO posts (author_id, kind, title, slug, status, published_at)
		VALUES ($1, 'article', 'Статья', 'gc-post', 'published', now()) RETURNING id::text`,
		rig.ownerID).Scan(&postID); err != nil {
		t.Fatalf("post: %v", err)
	}

	referenced := map[string]func(uuid.UUID){
		"recipe_media": func(id uuid.UUID) {
			mustExec(t, rig, `INSERT INTO recipe_media (recipe_id, media_id) VALUES ($1::uuid, $2)`, recipeID, id)
		},
		"post_media": func(id uuid.UUID) {
			mustExec(t, rig, `INSERT INTO post_media (post_id, media_id) VALUES ($1::uuid, $2)`, postID, id)
		},
		"posts.cover_media_id": func(id uuid.UUID) {
			mustExec(t, rig, `UPDATE posts SET cover_media_id = $2 WHERE id = $1::uuid`, postID, id)
		},
		"users.avatar_media_id": func(id uuid.UUID) {
			mustExec(t, rig, `UPDATE users SET avatar_media_id = $2 WHERE id = $1`, rig.ownerID, id)
		},
		"users.cover_media_id": func(id uuid.UUID) {
			mustExec(t, rig, `UPDATE users SET cover_media_id = $2 WHERE id = $1`, rig.ownerID, id)
		},
		"body_blocks photo": func(id uuid.UUID) {
			mustExec(t, rig, `UPDATE posts SET body_blocks =
				jsonb_build_array(jsonb_build_object('type','photo','media_id', $2::text))
				WHERE id = $1::uuid`, rig.post(t, "gc-blocks-photo"), id)
		},
		"body_blocks gallery": func(id uuid.UUID) {
			mustExec(t, rig, `UPDATE posts SET body_blocks =
				jsonb_build_array(jsonb_build_object('type','gallery','media_ids',
				    jsonb_build_array($2::text)))
				WHERE id = $1::uuid`, rig.post(t, "gc-blocks-gallery"), id)
		},
	}

	keys := map[string]string{}
	for name, attach := range referenced {
		key := "kept/" + name + ".jpg"
		keys[name] = key
		attach(rig.asset(t, key))
	}
	// One with nothing pointing at it, to prove the sweep is running at all.
	rig.asset(t, "orphan/gone.jpg")

	if _, err := c.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	for name, key := range keys {
		if !rig.has(key) {
			t.Errorf("collector deleted an asset referenced by %s", name)
		}
	}
	if rig.has("orphan/gone.jpg") {
		t.Error("the unreferenced asset was not collected; the sweep did nothing")
	}
}

// TestGCFinishesAnInterruptedSweep: the row is marked before the objects go, so
// a crash in between leaves work the next run must finish rather than bytes
// nobody has the keys for.
func TestGCFinishesAnInterruptedSweep(t *testing.T) {
	rig, c := gcRig(t)
	ctx := context.Background()
	id := rig.asset(t, "half/done.jpg")

	// Simulate the crash: marked, objects still present.
	if _, err := rig.pool.Exec(ctx,
		`UPDATE media_assets SET status='orphaned' WHERE id=$1`, id); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if _, err := c.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if rig.has("half/done.jpg") {
		t.Error("an interrupted sweep was not finished")
	}
}

// TestGCKeepsRowWhenStorageFails: dropping the row after a failed delete would
// leak the bytes with no record of the key.
func TestGCKeepsRowWhenStorageFails(t *testing.T) {
	rig := newFlowRig(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	c := NewCollector(rig.repo, failingDeleter{}, quiet)
	c.Grace = 0

	id := rig.asset(t, "stuck/a.jpg")
	n, err := c.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 0 {
		t.Errorf("reported %d collected although storage refused", n)
	}
	var status string
	if err := rig.pool.QueryRow(context.Background(),
		`SELECT status FROM media_assets WHERE id=$1`, id).Scan(&status); err != nil {
		t.Fatalf("the row was deleted despite the object surviving: %v", err)
	}
	if status != "orphaned" {
		t.Errorf("status = %q, want orphaned so the next sweep retries", status)
	}
}

type failingDeleter struct{}

func (failingDeleter) Delete(context.Context, string) error {
	return errStorageDown
}

var errStorageDown = objstore.ErrNotFound

func mustExec(t *testing.T, rig *flowRig, sql string, args ...any) {
	t.Helper()
	if _, err := rig.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

// post creates an empty article to hang a reference on.
func (r *flowRig) post(t *testing.T, slug string) string {
	t.Helper()
	var id string
	if err := r.pool.QueryRow(context.Background(), `
		INSERT INTO posts (author_id, kind, title, slug, status, published_at)
		VALUES ($1, 'article', $2, $2, 'published', now()) RETURNING id::text`,
		r.ownerID, slug).Scan(&id); err != nil {
		t.Fatalf("create post: %v", err)
	}
	return id
}
