-- Durable background work (media transcoding, counter rollups, cleanups).
--
-- Postgres is the queue rather than a separate broker: the workload is modest,
-- the jobs must be transactional with the rows that spawn them, and one fewer
-- moving part is one fewer thing to operate. FOR UPDATE SKIP LOCKED scales to
-- far more throughput than this store will produce for a long time.

CREATE TABLE jobs (
    id           bigserial PRIMARY KEY,
    kind         text NOT NULL,
    payload      jsonb NOT NULL DEFAULT '{}'::jsonb,
    status       text NOT NULL DEFAULT 'queued'
                 CHECK (status IN ('queued', 'running', 'done', 'failed', 'dead')),
    priority     smallint NOT NULL DEFAULT 100,
    attempts     int NOT NULL DEFAULT 0,
    max_attempts int NOT NULL DEFAULT 5,
    run_after    timestamptz NOT NULL DEFAULT now(),
    locked_at    timestamptz,
    locked_by    text,
    last_error   text,
    -- Dedupe key: enqueueing the same logical work twice is a no-op while the
    -- first copy is still outstanding.
    dedupe_key   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz
);

-- The claim query orders by (priority, run_after, id) over queued rows only.
CREATE INDEX jobs_claim_idx ON jobs (priority, run_after, id)
    WHERE status = 'queued';
CREATE INDEX jobs_status_idx ON jobs (status, updated_at DESC);
CREATE UNIQUE INDEX jobs_dedupe_key_idx ON jobs (dedupe_key)
    WHERE dedupe_key IS NOT NULL AND status IN ('queued', 'running');
-- Stuck-job sweeper looks here: running rows whose lock has gone stale.
CREATE INDEX jobs_stale_lock_idx ON jobs (locked_at) WHERE status = 'running';

CREATE TRIGGER jobs_touch BEFORE UPDATE ON jobs
    FOR EACH ROW EXECUTE FUNCTION sk_touch_updated_at();
