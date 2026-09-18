-- Author leaderboard and the ranked shorts feed.
--
-- A plain table, not a materialized view: REFRESH CONCURRENTLY needs a unique
-- index, takes a lock, cannot be incremental, and has nowhere to keep the
-- previous rank -- which is the one thing that lets the UI show movement
-- instead of a bare number.

CREATE TABLE author_ranks (
    user_id          uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    score            numeric(10,6) NOT NULL,
    rank             int NOT NULL,
    prev_rank        int,

    -- The inputs are stored alongside the result so a surprising rank can be
    -- explained without recomputing it.
    imports_30d      int NOT NULL DEFAULT 0,
    rating_avg_30d   numeric(3,2),
    rating_count_30d int NOT NULL DEFAULT 0,
    followers_30d    int NOT NULL DEFAULT 0,

    window_days      smallint NOT NULL DEFAULT 30,
    computed_at      timestamptz NOT NULL DEFAULT now()
);

-- The leaderboard listing pages by rank; both columns go the same way so the
-- row-wise keyset comparison matches the index.
CREATE INDEX author_ranks_rank_idx ON author_ranks (rank ASC, user_id ASC);

-- The badge lookup is "is this author in the top N", which is a handful of
-- rows and wants its own tiny index.
CREATE INDEX author_ranks_top_idx ON author_ranks (rank) WHERE rank <= 100;

-- feed_epoch on posts (added in 0008) is stamped from here so a cursor minted
-- before a recompute can be told apart from one minted after.
CREATE TABLE ranking_state (
    id          boolean PRIMARY KEY DEFAULT true CHECK (id),
    epoch       bigint NOT NULL DEFAULT 0,
    computed_at timestamptz
);
INSERT INTO ranking_state (id, epoch) VALUES (true, 0) ON CONFLICT DO NOTHING;
