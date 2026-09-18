package author

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/search"
)

// rename moves an author to a new handle the way the studio endpoint will:
// record the old one, then change it.
func rename(t *testing.T, r *rig, userID, from, to string) {
	t.Helper()
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO user_handle_history (old_handle, user_id) VALUES ($1, $2::uuid)
		 ON CONFLICT (old_handle) DO UPDATE SET user_id = EXCLUDED.user_id, changed_at = now()`,
		from, userID); err != nil {
		t.Fatalf("record old handle: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE users SET handle = $2 WHERE id = $1::uuid`, userID, to); err != nil {
		t.Fatalf("rename: %v", err)
	}
}

func TestProfileByHandleAndByID(t *testing.T) {
	r := newRig(t)
	h := r.handler("")

	for _, path := range []string{"/v1/authors/chef_anna", "/v1/authors/" + r.anna} {
		rec := do(t, h, "GET", path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200; body: %s", path, rec.Code, rec.Body.String())
		}
		var p Profile
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if p.Handle != "chef_anna" || p.ID != r.anna {
			t.Errorf("GET %s returned %s/%s, want chef_anna/%s", path, p.Handle, p.ID, r.anna)
		}
		// Counters come from user_stats and start at zero until the publish
		// path maintains them; what matters here is that the block is present
		// and the profile does not fall over without it.
		if p.Links == nil {
			t.Error("links must serialize as [] rather than null")
		}
	}
}

// TestHandleIsCaseInsensitive: users.handle is citext, and a shared link may
// carry any casing.
func TestHandleIsCaseInsensitive(t *testing.T) {
	r := newRig(t)
	rec := do(t, r.handler(""), "GET", "/v1/authors/CHEF_ANNA")
	if rec.Code != http.StatusOK {
		t.Fatalf("mixed-case handle = %d, want 200", rec.Code)
	}
}

func TestProfileUnknownAuthor(t *testing.T) {
	r := newRig(t)
	h := r.handler("")
	for _, path := range []string{
		"/v1/authors/nobody_here",
		"/v1/authors/" + uuid.NewString(),
		"/v1/authors/!!!",
	} {
		if rec := do(t, h, "GET", path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
}

// TestRenamedAuthorKeepsOldLinks is why user_handle_history exists: a shared
// link must survive a rename.
func TestRenamedAuthorKeepsOldLinks(t *testing.T) {
	r := newRig(t)
	rename(t, r, r.anna, "chef_anna", "anna_cooks")

	rec := do(t, r.handler(""), "GET", "/v1/authors/chef_anna")
	if rec.Code != http.StatusOK {
		t.Fatalf("old handle = %d, want 200", rec.Code)
	}
	var p Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The response carries the canonical handle so the client can correct its URL.
	if p.Handle != "anna_cooks" {
		t.Errorf("handle = %q, want the current one (anna_cooks)", p.Handle)
	}
}

// TestFreedHandleOutranksHistory is the trap in handle history.
//
// Deleting an account frees its handle (migration 0001 does this deliberately
// for account deletion). If history were consulted first, the old owner would
// hijack the page of whoever registered the handle afterwards.
func TestFreedHandleOutranksHistory(t *testing.T) {
	r := newRig(t)
	rename(t, r, r.anna, "chef_anna", "anna_cooks")
	newcomer := r.user(t, "chef_anna", "Совсем другой человек", true)

	rec := do(t, r.handler(""), "GET", "/v1/authors/chef_anna")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200", rec.Code)
	}
	var p Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.ID != newcomer {
		t.Errorf("handle resolved to %s (old owner), want the live holder %s", p.ID, newcomer)
	}
}

func TestAuthorRecipeShelf(t *testing.T) {
	r := newRig(t)
	rec := do(t, r.handler(""), "GET", "/v1/authors/chef_anna/recipes")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var page search.Page
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("shelf has %d recipes, want 3 (Anna's only)", len(page.Items))
	}
	for _, c := range page.Items {
		if c.Author.Handle != "chef_anna" {
			t.Errorf("shelf leaked %q by %s", c.Title, c.Author.Handle)
		}
		// The byline must carry what the client needs to navigate here.
		if c.Author.ID == "" {
			t.Errorf("card %q has no author id to tap through to", c.Title)
		}
	}
}

// TestShelfIgnoresAuthorQueryParam: the path owns the author, so ?author= must
// not be able to page someone else's shelf under this URL.
func TestShelfIgnoresAuthorQueryParam(t *testing.T) {
	r := newRig(t)
	rec := do(t, r.handler(""), "GET", "/v1/authors/chef_anna/recipes?author=chef_boris")
	if rec.Code != http.StatusOK {
		t.Fatalf("= %d, want 200", rec.Code)
	}
	var page search.Page
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("got %d items, want Anna's 3", len(page.Items))
	}
}

func TestFollowRoundTrip(t *testing.T) {
	r := newRig(t)
	h := r.handler(r.reader)

	// Idempotent: the client cannot know whether the first tap landed.
	for i := range 2 {
		if rec := do(t, h, "PUT", "/v1/authors/chef_anna/follow"); rec.Code != http.StatusNoContent {
			t.Fatalf("follow #%d = %d, want 204", i+1, rec.Code)
		}
	}

	rec := do(t, h, "GET", "/v1/authors/chef_anna")
	var p Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !p.Viewer.Following {
		t.Error("viewer.following is false right after following")
	}
	if p.Stats.Followers != 1 {
		t.Errorf("followers = %d, want 1", p.Stats.Followers)
	}

	if rec := do(t, h, "DELETE", "/v1/authors/chef_anna/follow"); rec.Code != http.StatusNoContent {
		t.Fatalf("unfollow = %d, want 204", rec.Code)
	}
	rec = do(t, h, "GET", "/v1/authors/chef_anna")
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Viewer.Following || p.Stats.Followers != 0 {
		t.Errorf("after unfollow: following=%v followers=%d, want false/0",
			p.Viewer.Following, p.Stats.Followers)
	}
}

func TestFollowRejectsSelfAndAnonymous(t *testing.T) {
	r := newRig(t)

	rec := do(t, r.handler(r.anna), "PUT", "/v1/authors/chef_anna/follow")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("self-follow = %d, want 422", rec.Code)
	}
	rec = do(t, r.handler(""), "PUT", "/v1/authors/chef_anna/follow")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous follow = %d, want 401", rec.Code)
	}
}

// TestBlockHidesProfileBothWays: a block must work in both directions, or it
// is a one-way mirror the blocker still has to look through.
func TestBlockHidesProfileBothWays(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()

	// The reader follows Anna first, so we can check the block clears it.
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO follows (follower_id, author_id) VALUES ($1::uuid, $2::uuid)`,
		r.reader, r.anna); err != nil {
		t.Fatalf("seed follow: %v", err)
	}

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1::uuid, $2::uuid)`,
		r.reader, r.anna); err != nil {
		t.Fatalf("block: %v", err)
	}

	if rec := do(t, r.handler(r.reader), "GET", "/v1/authors/chef_anna"); rec.Code != http.StatusNotFound {
		t.Errorf("blocker viewing blocked author = %d, want 404", rec.Code)
	}
	if rec := do(t, r.handler(r.anna), "GET", "/v1/authors/just_reader"); rec.Code != http.StatusNotFound {
		t.Errorf("blocked author viewing blocker = %d, want 404", rec.Code)
	}
	// Everyone else is unaffected.
	if rec := do(t, r.handler(r.boris), "GET", "/v1/authors/chef_anna"); rec.Code != http.StatusOK {
		t.Errorf("unrelated viewer = %d, want 200", rec.Code)
	}

	var follows int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM follows`).Scan(&follows); err != nil {
		t.Fatalf("count follows: %v", err)
	}
	if follows != 0 {
		t.Errorf("block left %d follow rows behind; a blocked follower would keep getting updates", follows)
	}
}

func TestSuspendedAuthorIsInvisible(t *testing.T) {
	r := newRig(t)
	if _, err := r.pool.Exec(context.Background(),
		`UPDATE users SET status='suspended' WHERE id=$1::uuid`, r.anna); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if rec := do(t, r.handler(""), "GET", "/v1/authors/chef_anna"); rec.Code != http.StatusNotFound {
		t.Errorf("suspended author = %d, want 404", rec.Code)
	}
}

// TestLinksHiddenForUntrustedAuthors: outbound links on an unmoderated public
// page are a spam vector, so the trust ladder gates them.
func TestLinksHiddenForUntrustedAuthors(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	links := `[{"title":"Сайт","url":"https://example.com"}]`

	if _, err := r.pool.Exec(ctx,
		`UPDATE users SET links = $2::jsonb, trust_level = 0 WHERE id = $1::uuid`,
		r.anna, links); err != nil {
		t.Fatalf("set links: %v", err)
	}
	rec := do(t, r.handler(""), "GET", "/v1/authors/chef_anna")
	var p Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(p.Links) != 0 {
		t.Errorf("newcomer's links are shown: %+v", p.Links)
	}

	if _, err := r.pool.Exec(ctx,
		`UPDATE users SET trust_level = 1 WHERE id = $1::uuid`, r.anna); err != nil {
		t.Fatalf("promote: %v", err)
	}
	rec = do(t, r.handler(""), "GET", "/v1/authors/chef_anna")
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(p.Links) != 1 || p.Links[0].URL != "https://example.com" {
		t.Errorf("trusted author's links = %+v, want the one link", p.Links)
	}
}

func TestSanitizeLinkRejectsScriptSchemes(t *testing.T) {
	// A javascript: or data: URL rendered as a profile link is an XSS delivery
	// mechanism on whichever client opens it.
	for _, bad := range []string{
		"javascript:alert(1)", "data:text/html,<script>", "file:///etc/passwd", "nonsense", "",
	} {
		if _, ok := SanitizeLink(Link{Title: "x", URL: bad}); ok {
			t.Errorf("SanitizeLink accepted %q", bad)
		}
	}
	got, ok := SanitizeLink(Link{URL: "https://example.com/blog"})
	if !ok || got.Title != "example.com" {
		t.Errorf("SanitizeLink(%q) = %+v, %v; want host as fallback title", "https://example.com/blog", got, ok)
	}
}

func TestProfileETagAndPrivacy(t *testing.T) {
	r := newRig(t)

	rec := do(t, r.handler(""), "GET", "/v1/authors/chef_anna")
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on an anonymous profile")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("anonymous profile carries Cache-Control %q", cc)
	}

	// A signed-in reader's body carries their follow state, so it must not be
	// storable in any shared cache.
	rec = do(t, r.handler(r.reader), "GET", "/v1/authors/chef_anna")
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("signed-in profile Cache-Control = %q, want private, no-store", cc)
	}
}

func TestFeaturedRecipeMustBelongToAuthor(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()

	var borisRecipe string
	if err := r.pool.QueryRow(ctx,
		`SELECT id::text FROM recipes WHERE slug = 'boris-0'`).Scan(&borisRecipe); err != nil {
		t.Fatalf("find recipe: %v", err)
	}
	// Point Anna's featured slot at someone else's recipe. Nothing in the
	// schema forbids it, so the read path has to.
	if _, err := r.pool.Exec(ctx,
		`UPDATE users SET featured_recipe_id = $2::uuid WHERE id = $1::uuid`,
		r.anna, borisRecipe); err != nil {
		t.Fatalf("set featured: %v", err)
	}

	rec := do(t, r.handler(""), "GET", "/v1/authors/chef_anna")
	var p Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Featured != nil {
		t.Errorf("profile featured someone else's recipe: %q by %s",
			p.Featured.Title, p.Featured.Author.Handle)
	}

	// The author's own recipe is shown.
	var own string
	if err := r.pool.QueryRow(ctx,
		`SELECT id::text FROM recipes WHERE slug = 'anna-0'`).Scan(&own); err != nil {
		t.Fatalf("find recipe: %v", err)
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE users SET featured_recipe_id = $2::uuid WHERE id = $1::uuid`, r.anna, own); err != nil {
		t.Fatalf("set featured: %v", err)
	}
	rec = do(t, r.handler(""), "GET", "/v1/authors/chef_anna")
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Featured == nil || p.Featured.Title != "Борщ" {
		t.Errorf("featured = %+v, want Борщ", p.Featured)
	}
	_ = mustUUID(t, own)
}
