package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/jobs"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/objstore"
	"github.com/5f59cbfv7m-maker/sashas-kitchen-store/internal/postgres"
)

// flowPool provisions this package's own database. Tests that write to shared
// tables must not race the other packages' suites, which run in parallel.
func flowPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping database-backed test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	target := strings.TrimPrefix(u.Path, "/") + "_media"

	admin := *u
	admin.Path = "/postgres"
	conn, err := pgx.Connect(ctx, admin.String())
	if err != nil {
		t.Fatalf("connect maintenance db: %v", err)
	}
	var exists bool
	if err := conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)`, target).Scan(&exists); err != nil {
		t.Fatalf("check db: %v", err)
	}
	if !exists {
		if _, err := conn.Exec(ctx, `CREATE DATABASE "`+target+`"`); err != nil &&
			!strings.Contains(err.Error(), "already exists") {
			t.Fatalf("create db: %v", err)
		}
	}
	_ = conn.Close(context.WithoutCancel(ctx))

	out := *u
	out.Path = "/" + target
	pool, err := pgxpool.New(ctx, out.String())
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := postgres.Migrate(ctx, pool, quiet); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// flowRig is everything needed to drive one upload through the pipeline.
type flowRig struct {
	pool    *pgxpool.Pool
	repo    *Repo
	store   *objstore.Fake
	queue   *jobs.Queue
	svc     *Service
	coder   *FakeTranscoder
	worker  *Worker
	runner  *jobs.Runner
	ownerID uuid.UUID
}

func newFlowRig(t *testing.T) *flowRig {
	t.Helper()
	pool := flowPool(t)

	var ownerID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (handle, display_name, is_author)
		VALUES ('media_'||$1, 'Автор', true) RETURNING id`,
		fmt.Sprint(time.Now().UnixNano())).Scan(&ownerID); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, ownerID)
	})

	// The jobs table outlives a test run, and a leftover queued job would be
	// claimed by the next test's ProcessBatch instead of its own. This package
	// owns its database, so clearing the queue here is safe.
	if _, err := pool.Exec(context.Background(), `DELETE FROM jobs`); err != nil {
		t.Fatalf("clear job queue: %v", err)
	}

	repo := NewRepo(pool)
	store := objstore.NewFake()
	queue := jobs.NewQueue(pool)
	coder := &FakeTranscoder{}
	quiet := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))

	rig := &flowRig{
		pool: pool, repo: repo, store: store, queue: queue,
		svc:     NewService(repo, store, queue),
		coder:   coder,
		worker:  NewWorker(repo, store, coder, quiet),
		ownerID: ownerID,
	}
	rig.runner = jobs.NewRunner("test-worker", queue, quiet)
	rig.runner.Handle(JobKindTranscode, rig.worker.Handler())
	return rig
}

// has reports whether the object store holds key.
func (r *flowRig) has(key string) bool {
	_, err := r.store.Stat(context.Background(), key)
	return err == nil
}

// uploadPhoto walks a photo to the "uploaded" state, simulating the client
// PUTting bytes straight to the bucket.
func (r *flowRig) uploadPhoto(t *testing.T, body []byte, contentType string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	ticket, err := r.svc.CreateUploadTicket(ctx, r.ownerID, KindPhoto, contentType, int64(len(body)))
	if err != nil {
		t.Fatalf("CreateUploadTicket: %v", err)
	}
	asset, err := r.repo.Get(ctx, ticket.AssetID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	r.store.Seed(asset.StorageKey, body, contentType)
	return ticket.AssetID
}

// TestUploadToReady covers the happy path all the way to a ready asset.
func TestUploadToReady(t *testing.T) {
	rig := newFlowRig(t)
	ctx := context.Background()

	id := rig.uploadPhoto(t, []byte("fake-jpeg-bytes"), "image/jpeg")

	rig.coder.PhotoResult = TranscodeResult{
		Width: 1200, Height: 800, Blurhash: "LEHV6nWB2yk8",
		Derivatives: []Derivative{
			{Key: PhotoDerivativeKey(id, "card"), ContentType: "image/jpeg", Data: []byte("card")},
		},
	}

	if _, err := rig.svc.CompleteUpload(ctx, id, rig.ownerID); err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	asset, _ := rig.repo.Get(ctx, id)
	if asset.Status != StatusUploaded {
		t.Fatalf("after complete: got %s, want uploaded", asset.Status)
	}

	n, err := rig.runner.ProcessBatch(ctx)
	if err != nil {
		t.Fatalf("ProcessBatch: %v", err)
	}
	if n != 1 {
		t.Fatalf("processed %d jobs, want 1", n)
	}

	asset, _ = rig.repo.Get(ctx, id)
	if asset.Status != StatusReady {
		t.Fatalf("after transcode: got %s, want ready", asset.Status)
	}
	if asset.Width == nil || *asset.Width != 1200 {
		t.Errorf("width not recorded: %v", asset.Width)
	}
	if !rig.has(PhotoDerivativeKey(id, "card")) {
		t.Error("derivative was not uploaded to the store")
	}
}

// TestTranscodeIsIdempotent is the property that matters most: the queue is
// at-least-once, so a redelivered job must not re-transcode or corrupt a
// finished asset.
func TestTranscodeIsIdempotent(t *testing.T) {
	rig := newFlowRig(t)
	ctx := context.Background()

	id := rig.uploadPhoto(t, []byte("bytes"), "image/jpeg")
	rig.coder.PhotoResult = TranscodeResult{Width: 100, Height: 100}
	if _, err := rig.svc.CompleteUpload(ctx, id, rig.ownerID); err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	if _, err := rig.runner.ProcessBatch(ctx); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if rig.coder.PhotoCalls != 1 {
		t.Fatalf("first pass transcoded %d times, want 1", rig.coder.PhotoCalls)
	}

	// Re-run the same asset directly, as a redelivered job would.
	if err := rig.worker.Transcode(ctx, id); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if rig.coder.PhotoCalls != 1 {
		t.Errorf("re-run transcoded again: %d calls, want 1", rig.coder.PhotoCalls)
	}
	asset, _ := rig.repo.Get(ctx, id)
	if asset.Status != StatusReady {
		t.Errorf("re-run changed status to %s", asset.Status)
	}
}

// TestTranscodeFailureMarksAsset covers the unhappy path.
func TestTranscodeFailureMarksAsset(t *testing.T) {
	rig := newFlowRig(t)
	ctx := context.Background()

	id := rig.uploadPhoto(t, []byte("bytes"), "image/jpeg")
	if _, err := rig.svc.CompleteUpload(ctx, id, rig.ownerID); err != nil {
		t.Fatalf("CompleteUpload: %v", err)
	}
	rig.coder.Err = errors.New("ffmpeg exploded")

	if _, err := rig.runner.ProcessBatch(ctx); err != nil {
		t.Fatalf("ProcessBatch returned error: %v", err)
	}
	asset, _ := rig.repo.Get(ctx, id)
	if asset.Status != StatusFailed {
		t.Errorf("after failure: got %s, want failed", asset.Status)
	}
	if asset.Error == nil || *asset.Error == "" {
		t.Error("failure did not record a reason")
	}
}

// TestCompleteRejectsOversizedUpload proves the server does not take the
// client's word for the size: the ticket declares 10 bytes, the bucket holds
// far more.
func TestCompleteRejectsOversizedUpload(t *testing.T) {
	rig := newFlowRig(t)
	ctx := context.Background()

	ticket, err := rig.svc.CreateUploadTicket(ctx, rig.ownerID, KindPhoto, "image/jpeg", 10)
	if err != nil {
		t.Fatalf("CreateUploadTicket: %v", err)
	}
	asset, _ := rig.repo.Get(ctx, ticket.AssetID)
	// 30 MiB against a 25 MiB photo cap.
	rig.store.Seed(asset.StorageKey, make([]byte, 30<<20), "image/jpeg")

	_, err = rig.svc.CompleteUpload(ctx, ticket.AssetID, rig.ownerID)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized upload: got %v, want ErrTooLarge", err)
	}
	if rig.has(asset.StorageKey) {
		t.Error("rejected upload was left squatting in the bucket")
	}
}

// TestCompleteRejectsForeignAsset checks ownership is enforced.
func TestCompleteRejectsForeignAsset(t *testing.T) {
	rig := newFlowRig(t)
	ctx := context.Background()

	id := rig.uploadPhoto(t, []byte("bytes"), "image/jpeg")
	_, err := rig.svc.CompleteUpload(ctx, id, uuid.New())
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("completing someone else's upload: got %v, want ErrForbidden", err)
	}
}

// TestCompleteRequiresActualUpload rejects a client that claims completion
// without ever PUTting the bytes.
func TestCompleteRequiresActualUpload(t *testing.T) {
	rig := newFlowRig(t)
	ctx := context.Background()

	ticket, err := rig.svc.CreateUploadTicket(ctx, rig.ownerID, KindPhoto, "image/jpeg", 100)
	if err != nil {
		t.Fatalf("CreateUploadTicket: %v", err)
	}
	if _, err := rig.svc.CompleteUpload(ctx, ticket.AssetID, rig.ownerID); !errors.Is(err, ErrNotUploaded) {
		t.Errorf("completing an empty upload: got %v, want ErrNotUploaded", err)
	}
}

// TestUnsupportedTypeRejected covers the allow-list.
func TestUnsupportedTypeRejected(t *testing.T) {
	rig := newFlowRig(t)
	_, err := rig.svc.CreateUploadTicket(context.Background(), rig.ownerID,
		KindPhoto, "application/x-msdownload", 100)
	if !errors.Is(err, ErrUnsupportedType) {
		t.Errorf("executable upload: got %v, want ErrUnsupportedType", err)
	}
}

// TestDoubleCompleteEnqueuesOneJob checks the dedupe key holds.
func TestDoubleCompleteEnqueuesOneJob(t *testing.T) {
	rig := newFlowRig(t)
	ctx := context.Background()

	id := rig.uploadPhoto(t, []byte("bytes"), "image/jpeg")
	for i := 0; i < 3; i++ {
		if _, err := rig.svc.CompleteUpload(ctx, id, rig.ownerID); err != nil {
			t.Fatalf("CompleteUpload %d: %v", i, err)
		}
	}
	var n int
	if err := rig.pool.QueryRow(ctx, `
		SELECT count(*)::int FROM jobs
		 WHERE kind=$1 AND payload->>'asset_id' = $2 AND status IN ('queued','running')`,
		JobKindTranscode, id.String()).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n != 1 {
		t.Errorf("three completions queued %d jobs, want 1", n)
	}
}
