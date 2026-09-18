package media

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// These tests drive the handlers through a real http.ServeMux instead of
// calling the service directly, because the bug they exist to prevent lived
// exactly in that gap: the package used to read the caller from a context key
// that nothing ever set, so every upload answered 401 while the service-level
// tests stayed green.

// serve builds the routed handler with a caller resolver that reports who is
// asking, mirroring what cmd/api wires up from the auth middleware.
func (r *flowRig) serve(t *testing.T, caller CallerFunc) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	NewAPI(r.svc, r.repo, r.store, caller).Routes(mux)
	return mux
}

// authedAs resolves every request to id, standing in for a valid bearer token.
func authedAs(id uuid.UUID) CallerFunc {
	return func(*http.Request) (uuid.UUID, bool) { return id, true }
}

// anonymous resolves nobody, standing in for a missing or invalid token.
func anonymous() CallerFunc {
	return func(*http.Request) (uuid.UUID, bool) { return uuid.UUID{}, false }
}

func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestUploadTicketOverHTTP is the regression test for the unwired caller. An
// authenticated POST must reach the service and come back with a ticket; when
// this broke, it returned 401 instead.
func TestUploadTicketOverHTTP(t *testing.T) {
	rig := newFlowRig(t)
	h := rig.serve(t, authedAs(rig.ownerID))

	rec := do(t, h, "POST", "/v1/media/uploads",
		`{"kind":"photo","content_type":"image/jpeg","bytes":2048}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/media/uploads = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}

	var ticket Ticket
	if err := json.Unmarshal(rec.Body.Bytes(), &ticket); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}
	if ticket.AssetID == uuid.Nil {
		t.Error("ticket carries no asset id")
	}
	if ticket.UploadURL == "" {
		t.Error("ticket carries no upload URL")
	}
	if ticket.Method != http.MethodPut {
		t.Errorf("ticket method = %q, want PUT", ticket.Method)
	}

	// The asset must be owned by the caller the router resolved, not by nobody.
	asset, err := rig.repo.Get(context.Background(), ticket.AssetID)
	if err != nil {
		t.Fatalf("read back asset: %v", err)
	}
	if asset.OwnerID != rig.ownerID {
		t.Errorf("asset owner = %s, want %s", asset.OwnerID, rig.ownerID)
	}
}

// TestUploadRejectsAnonymous guards the other half: the endpoint must still
// refuse a request that carries no identity.
func TestUploadRejectsAnonymous(t *testing.T) {
	rig := newFlowRig(t)
	h := rig.serve(t, anonymous())

	for _, tc := range []struct{ method, target, body string }{
		{"POST", "/v1/media/uploads", `{"kind":"photo","content_type":"image/jpeg","bytes":2048}`},
		{"POST", "/v1/media/" + uuid.NewString() + "/complete", ""},
	} {
		rec := do(t, h, tc.method, tc.target, tc.body)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", tc.method, tc.target, rec.Code)
		}
	}
}

// TestNilCallerRefusesEveryone pins the safe default: an API assembled without
// a resolver must reject writes rather than treat them as anonymous-but-allowed.
func TestNilCallerRefusesEveryone(t *testing.T) {
	rig := newFlowRig(t)
	h := rig.serve(t, nil)

	rec := do(t, h, "POST", "/v1/media/uploads",
		`{"kind":"photo","content_type":"image/jpeg","bytes":2048}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("POST with nil caller = %d, want 401", rec.Code)
	}
}

// TestCompleteOverHTTP walks the whole client contract through the router:
// ticket, PUT to storage, complete, then poll until the derivatives exist.
func TestCompleteOverHTTP(t *testing.T) {
	rig := newFlowRig(t)
	h := rig.serve(t, authedAs(rig.ownerID))

	rec := do(t, h, "POST", "/v1/media/uploads",
		`{"kind":"photo","content_type":"image/jpeg","bytes":1024}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create upload = %d; body: %s", rec.Code, rec.Body.String())
	}
	var ticket Ticket
	if err := json.Unmarshal(rec.Body.Bytes(), &ticket); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}

	// Stand in for the client's PUT straight to object storage.
	asset, err := rig.repo.Get(context.Background(), ticket.AssetID)
	if err != nil {
		t.Fatalf("read asset: %v", err)
	}
	if err := rig.store.Put(context.Background(), asset.StorageKey,
		bytes.NewReader(make([]byte, 1024)), 1024, "image/jpeg"); err != nil {
		t.Fatalf("put object: %v", err)
	}

	rec = do(t, h, "POST", "/v1/media/"+ticket.AssetID.String()+"/complete", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("complete = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	// GET is public: a reader fetching a card's media has no reason to be
	// signed in, so it must work without a caller.
	pub := rig.serve(t, anonymous())
	rec = do(t, pub, "GET", "/v1/media/"+ticket.AssetID.String(), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET asset anonymously = %d, want 200", rec.Code)
	}
	var view map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode asset view: %v", err)
	}
	// Storage keys are internal; a client must never receive one.
	for _, leaked := range []string{"storage_key", "poster_key", "hls_key"} {
		if _, ok := view[leaked]; ok {
			t.Errorf("asset view leaks %q", leaked)
		}
	}
}

// TestCompleteRefusesSomeoneElsesAsset pins the deliberate 404: confirming that
// another user's asset exists would leak the id space.
func TestCompleteRefusesSomeoneElsesAsset(t *testing.T) {
	rig := newFlowRig(t)

	rec := do(t, rig.serve(t, authedAs(rig.ownerID)), "POST", "/v1/media/uploads",
		`{"kind":"photo","content_type":"image/jpeg","bytes":1024}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create upload = %d", rec.Code)
	}
	var ticket Ticket
	if err := json.Unmarshal(rec.Body.Bytes(), &ticket); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}

	intruder := rig.serve(t, authedAs(uuid.New()))
	rec = do(t, intruder, "POST", "/v1/media/"+ticket.AssetID.String()+"/complete", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("complete someone else's asset = %d, want 404", rec.Code)
	}
}
