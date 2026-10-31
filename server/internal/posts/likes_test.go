package posts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func send(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestLikesCountOncePerPersonAndShowInFeed(t *testing.T) {
	r := newRig(t)
	id := r.short(t, r.anna, "Шортс", true, 1)

	if rec := send(t, r.handler(""), "PUT", "/v1/posts/"+id+"/like"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous like = %d, want 401", rec.Code)
	}
	for range 2 { // a retried like must not count twice
		if rec := send(t, r.handler(r.reader), "PUT", "/v1/posts/"+id+"/like"); rec.Code != http.StatusOK {
			t.Fatalf("like = %d: %s", rec.Code, rec.Body.String())
		}
	}
	rec := send(t, r.handler(r.boris), "PUT", "/v1/posts/"+id+"/like")
	var liked likeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &liked); err != nil || liked.LikeCount != 2 || !liked.Liked {
		t.Fatalf("second person's like = %+v (%v), want count 2", liked, err)
	}

	var page Page
	if err := json.Unmarshal(do(t, r.handler(r.reader), "/v1/shorts").Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].LikeCount != 2 || !page.Items[0].Liked {
		t.Fatalf("feed card = %+v, want like_count 2 and liked", page.Items)
	}
	var anonymous Page
	if err := json.Unmarshal(do(t, r.handler(""), "/v1/shorts").Body.Bytes(), &anonymous); err != nil || anonymous.Items[0].Liked {
		t.Errorf("anonymous feed says liked: %+v", anonymous.Items)
	}

	rec = send(t, r.handler(r.reader), "DELETE", "/v1/posts/"+id+"/like")
	if err := json.Unmarshal(rec.Body.Bytes(), &liked); err != nil || liked.LikeCount != 1 || liked.Liked {
		t.Fatalf("unlike = %+v, want count 1", liked)
	}
}

func TestViewsAndLikesNeedAVisiblePost(t *testing.T) {
	r := newRig(t)
	id := r.short(t, r.anna, "Шортс", true, 1)
	for range 3 {
		if rec := send(t, r.handler(""), "POST", "/v1/posts/"+id+"/view"); rec.Code != http.StatusNoContent {
			t.Fatalf("view = %d", rec.Code)
		}
	}
	var page Page
	if err := json.Unmarshal(do(t, r.handler(""), "/v1/shorts").Body.Bytes(), &page); err != nil || page.Items[0].ViewCount != 3 {
		t.Fatalf("view_count = %+v, want 3", page.Items)
	}

	if _, err := r.pool.Exec(t.Context(),
		`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1::uuid, $2::uuid)`, r.reader, r.anna); err != nil {
		t.Fatalf("block: %v", err)
	}
	if rec := send(t, r.handler(r.reader), "PUT", "/v1/posts/"+id+"/like"); rec.Code != http.StatusNotFound {
		t.Errorf("like on a blocked author's short = %d, want 404", rec.Code)
	}
	if rec := send(t, r.handler(""), "POST", "/v1/posts/00000000-0000-0000-0000-000000000000/view"); rec.Code != http.StatusNotFound {
		t.Errorf("view of a missing post = %d, want 404", rec.Code)
	}
}
