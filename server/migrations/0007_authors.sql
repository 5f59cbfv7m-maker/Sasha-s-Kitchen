-- Author pages: public profile, follows, per-author listing indexes.
--
-- Also repairs recipes.has_video, which has been wrong since 0002. See the
-- media_assets trigger at the bottom of this file.

-- ------------------------------------------------------- profile on users --

-- Stable, rarely written profile fields live on users. Volatile counters do
-- not: see user_stats below.
ALTER TABLE users
    ADD COLUMN cover_media_id     uuid REFERENCES media_assets(id) ON DELETE SET NULL,
    ADD COLUMN featured_recipe_id uuid REFERENCES recipes(id) ON DELETE SET NULL,
    -- posts does not exist until 0008, so the column is declared here and the
    -- foreign key is added there. 0002 solves the same ordering problem the
    -- same way with users_avatar_fk.
    ADD COLUMN featured_post_id   uuid,
    ADD COLUMN location           text CHECK (location IS NULL OR length(location) <= 80),
    -- Outbound links on a public page are a spam vector, so they are bounded
    -- here and only rendered for trusted authors by the API.
    ADD COLUMN links              jsonb NOT NULL DEFAULT '[]'::jsonb
                                  CHECK (jsonb_typeof(links) = 'array'
                                         AND jsonb_array_length(links) <= 8),
    ADD COLUMN trust_level        smallint NOT NULL DEFAULT 0
                                  CHECK (trust_level BETWEEN -1 AND 2),
    ADD COLUMN verified_at        timestamptz;

-- The storefront filters out suspended authors on every read (builder.go), and
-- the author directory needs to list only real authors. Neither has an index.
CREATE INDEX users_active_authors_idx ON users (created_at DESC, id DESC)
    WHERE status = 'active' AND deleted_at IS NULL AND is_author;

-- ------------------------------------------------------------ trust ladder --

-- Quotas live in a table, not in Go, for the same reason categories do: they
-- will be tuned in the first week and tuning must not need a deploy.
CREATE TABLE trust_levels (
    level              smallint PRIMARY KEY CHECK (level BETWEEN -1 AND 2),
    title              text NOT NULL,
    premoderate        boolean NOT NULL,
    daily_publications smallint NOT NULL CHECK (daily_publications >= 0),
    max_video_seconds  int      NOT NULL CHECK (max_video_seconds > 0),
    may_show_links     boolean  NOT NULL DEFAULT false
);

INSERT INTO trust_levels (level, title, premoderate, daily_publications, max_video_seconds, may_show_links) VALUES
    (-1, 'Ограниченный', true,   1,   60, false),
    ( 0, 'Новичок',      true,   3,   60, false),
    ( 1, 'Доверенный',   false, 10,  600, true),
    ( 2, 'Проверенный',  false, 30, 1800, true);

-- ------------------------------------------------------------ user_stats --

-- Counters are split off users deliberately.
--
-- users is re-read on every authenticated request and carries two unique
-- indexes the auth path walks. It is also at the default fillfactor, so once
-- pages fill, HOT updates stop applying and every counter bump starts writing
-- index entries on the hottest read path in the service. On top of that,
-- users_touch would move updated_at on every follow, destroying it as a
-- "profile was edited" signal.
--
-- fillfactor leaves room for HOT updates on the rows that actually churn.
CREATE TABLE user_stats (
    user_id         uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    recipe_count    int    NOT NULL DEFAULT 0,
    post_count      int    NOT NULL DEFAULT 0,
    short_count     int    NOT NULL DEFAULT 0,
    follower_count  int    NOT NULL DEFAULT 0,
    following_count int    NOT NULL DEFAULT 0,
    import_total    bigint NOT NULL DEFAULT 0,
    rating_avg      numeric(3,2),
    rating_count    int    NOT NULL DEFAULT 0,
    approved_count  smallint NOT NULL DEFAULT 0,
    recomputed_at   timestamptz
) WITH (fillfactor = 70);

COMMENT ON COLUMN user_stats.import_total IS
    'Refreshed by the periodic job, never by a trigger: a trigger here would '
    'serialize every import of every one of this author''s recipes on one row.';
COMMENT ON COLUMN user_stats.rating_avg IS
    'Refreshed by the periodic job, for the same reason as import_total.';
COMMENT ON COLUMN user_stats.approved_count IS
    'Publications approved by a moderator, counted to promote an author out of '
    'premoderation. Reset to 0 when an upheld report demotes them.';

-- The row must always exist, or every counter update becomes an upsert and
-- every profile read becomes a LEFT JOIN with coalesce.
CREATE OR REPLACE FUNCTION sk_user_stats_ensure()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO user_stats (user_id) VALUES (NEW.id) ON CONFLICT DO NOTHING;
    RETURN NULL;
END;
$$;

CREATE TRIGGER users_stats_row AFTER INSERT ON users
    FOR EACH ROW EXECUTE FUNCTION sk_user_stats_ensure();

INSERT INTO user_stats (user_id) SELECT id FROM users ON CONFLICT DO NOTHING;

-- --------------------------------------------------------------- follows --

CREATE TABLE follows (
    follower_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    author_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (follower_id, author_id),
    CONSTRAINT follows_not_self CHECK (follower_id <> author_id)
);

-- Both directions are paged, so both carry the id tiebreaker the keyset
-- comparison needs. The primary key gives the prefix but not the order.
CREATE INDEX follows_author_idx   ON follows (author_id, created_at DESC, follower_id DESC);
CREATE INDEX follows_follower_idx ON follows (follower_id, created_at DESC, author_id DESC);

-- follows is the source of truth; the counters are a cache of it.
--
-- following_count is safe as a trigger: it is bounded by how fast one person
-- can tap. follower_count is not -- fan-in is unbounded, and a viral author
-- would serialize every new follower on one row. It is maintained here because
-- correctness now beats throughput we do not yet have, and the periodic job
-- added later both reconciles drift and is where this trigger arm gets dropped
-- when concurrency makes it necessary.
CREATE OR REPLACE FUNCTION sk_follows_apply()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE user_stats SET follower_count  = follower_count  + 1 WHERE user_id = NEW.author_id;
        UPDATE user_stats SET following_count = following_count + 1 WHERE user_id = NEW.follower_id;
    ELSE
        UPDATE user_stats SET follower_count  = greatest(follower_count  - 1, 0) WHERE user_id = OLD.author_id;
        UPDATE user_stats SET following_count = greatest(following_count - 1, 0) WHERE user_id = OLD.follower_id;
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER follows_apply AFTER INSERT OR DELETE ON follows
    FOR EACH ROW EXECUTE FUNCTION sk_follows_apply();

-- Blocking must not leave a subscription behind: a blocked follower would keep
-- receiving the blocker's new work in their feed.
CREATE OR REPLACE FUNCTION sk_block_drops_follow()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    DELETE FROM follows
     WHERE (follower_id = NEW.blocker_id AND author_id = NEW.blocked_id)
        OR (follower_id = NEW.blocked_id AND author_id = NEW.blocker_id);
    RETURN NULL;
END;
$$;

CREATE TRIGGER user_blocks_drop_follow AFTER INSERT ON user_blocks
    FOR EACH ROW EXECUTE FUNCTION sk_block_drops_follow();

-- ---------------------------------------------------------- handle history --

-- Renaming an author must not kill every link that was ever shared.
--
-- Careful: users_handle_active_key is partial on deleted_at IS NULL, so
-- deleting an account frees its handle for reuse (0001 says so deliberately).
-- History therefore records last-writer-wins and is only consulted AFTER a
-- live handle lookup misses -- otherwise an old row would hijack the page of
-- whoever registered the freed handle afterwards.
CREATE TABLE user_handle_history (
    old_handle citext PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    changed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX user_handle_history_user_idx ON user_handle_history (user_id, changed_at DESC);

-- ------------------------------------------------- author listing indexes --

-- The author page pages through published recipes newest first. Both columns
-- go the same way so the row-wise keyset comparison matches the index, and the
-- predicate matches the read path exactly.
--
-- This does not make the read index-only: the card projection is twenty-odd
-- columns plus a LATERAL join. What it removes is the sort node, leaving
-- exactly LIMIT+1 heap fetches.
CREATE INDEX recipes_author_feed_idx ON recipes (author_id, published_at DESC, id DESC)
    WHERE status = 'published' AND deleted_at IS NULL;

-- Superseded: not partial on status, and no id tiebreaker, so the feed could
-- not walk it. The dashboard uses recipes_author_status_idx instead. An index
-- nothing reads is pure write amplification; 0006 set the precedent.
DROP INDEX IF EXISTS recipes_author_pub_idx;

-- ------------------------------------------------------- has_video repair --

-- has_video is computed as "a ready video is attached", but until now nothing
-- recomputed it when an asset BECAME ready. The triggers from 0002 fire on
-- recipe_ingredients, recipe_diets and recipe_media only, and no Go code calls
-- sk_recipe_refresh_derived directly.
--
-- The normal order of work is: create the asset, attach it to the recipe,
-- upload, transcode, mark ready. The last step is the one that changes the
-- answer, and it was the one nobody watched -- so has_video stayed false
-- forever and the "с видео" filter silently excluded every video recipe.
CREATE OR REPLACE FUNCTION sk_media_status_changed()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    -- Only readiness changes the derived answer; other transitions are noise.
    IF (OLD.status = 'ready') IS DISTINCT FROM (NEW.status = 'ready') THEN
        PERFORM sk_recipe_refresh_derived(rm.recipe_id)
           FROM recipe_media rm
          WHERE rm.media_id = NEW.id;
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER media_assets_status_changed AFTER UPDATE OF status ON media_assets
    FOR EACH ROW EXECUTE FUNCTION sk_media_status_changed();

-- Repair the rows the missing trigger already got wrong.
SELECT sk_recipe_refresh_derived(r.id)
  FROM recipes r
 WHERE r.deleted_at IS NULL
   AND r.has_video IS DISTINCT FROM EXISTS (
       SELECT 1 FROM recipe_media rm JOIN media_assets m ON m.id = rm.media_id
        WHERE rm.recipe_id = r.id AND m.kind = 'video' AND m.status = 'ready');
