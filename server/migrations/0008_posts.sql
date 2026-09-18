-- Blog articles and short vertical videos.
--
-- One table, discriminated by kind. A short is a post whose body is a single
-- video block, so splitting them would duplicate moderation, reports, blocks,
-- drafts, quotas, slugs and search for a difference that lives in two index
-- predicates. The CHECK constraints below carry the per-kind rules, the same
-- way recipes_paid_needs_price does on recipes.

CREATE TABLE posts (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        text NOT NULL CHECK (kind IN ('article', 'short')),

    -- A short has a caption, not a headline, but it still needs one: an
    -- untitled short is unsearchable and has nothing to put in a share card.
    title       text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 140),
    excerpt     text CHECK (excerpt IS NULL OR length(excerpt) <= 400),

    -- Articles are addressed by slug inside an author's page; shorts are
    -- addressed by id and have none. Uniqueness is per author, not global:
    -- two people may both write "борщ".
    slug        text CHECK (slug IS NULL OR slug ~ '^[a-z0-9-]{1,80}$'),

    -- Typed blocks rather than markdown: a post interleaves text, photos,
    -- video and embedded recipe cards, and the embedded recipe is the whole
    -- point -- it is how a reader takes a recipe straight out of an article.
    body_blocks jsonb NOT NULL DEFAULT '[]'::jsonb
                CHECK (jsonb_typeof(body_blocks) = 'array'),
    -- Derived from body_blocks by trigger, below. A plain column, not
    -- GENERATED: Postgres forbids a generated column from reading another
    -- generated column, and search_vector has to read this one.
    body_text   text NOT NULL DEFAULT '',

    cover_media_id uuid REFERENCES media_assets(id) ON DELETE SET NULL,
    video_media_id uuid REFERENCES media_assets(id) ON DELETE SET NULL,
    -- Mirrors "the video has finished transcoding". A short whose HLS is not
    -- ready is a dead card in the feed, so the feed indexes exclude it.
    video_ready    boolean NOT NULL DEFAULT false,

    status          text NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft', 'review', 'published', 'rejected', 'archived')),
    published_at    timestamptz,
    rejected_reason text,

    -- Ranked feed position. Written ONLY by the ranking job, never on the read
    -- path: keyset pagination over a value that moves while the reader pages
    -- skips and repeats rows.
    feed_score  numeric(12,6) NOT NULL DEFAULT 0,
    feed_epoch  bigint NOT NULL DEFAULT 0,

    like_count    bigint NOT NULL DEFAULT 0,
    comment_count bigint NOT NULL DEFAULT 0,

    search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('russian', coalesce(title, '')),     'A') ||
        setweight(to_tsvector('russian', coalesce(excerpt, '')),   'B') ||
        setweight(to_tsvector('russian', coalesce(body_text, '')), 'C')
    ) STORED,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,

    CONSTRAINT posts_article_needs_slug CHECK (kind <> 'article' OR slug IS NOT NULL),
    CONSTRAINT posts_short_needs_video  CHECK (kind <> 'short'   OR video_media_id IS NOT NULL),
    CONSTRAINT posts_published_needs_time CHECK (status <> 'published' OR published_at IS NOT NULL)
);

CREATE UNIQUE INDEX posts_author_slug_key ON posts (author_id, slug)
    WHERE deleted_at IS NULL AND slug IS NOT NULL;

CREATE TRIGGER posts_touch BEFORE UPDATE ON posts
    FOR EACH ROW EXECUTE FUNCTION sk_touch_updated_at();

-- users.featured_post_id was declared in 0007; posts did not exist yet. 0002
-- adds users_avatar_fk the same way, after media_assets.
ALTER TABLE users
    ADD CONSTRAINT users_featured_post_fk FOREIGN KEY (featured_post_id)
    REFERENCES posts(id) ON DELETE SET NULL;

-- ------------------------------------------------------------ post media --

CREATE TABLE post_media (
    post_id  uuid NOT NULL REFERENCES posts(id)        ON DELETE CASCADE,
    media_id uuid NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    position int  NOT NULL DEFAULT 0,
    PRIMARY KEY (post_id, media_id)
);
CREATE INDEX post_media_post_idx  ON post_media (post_id, position);
CREATE INDEX post_media_media_idx ON post_media (media_id);

-- A post may point at recipes, which is what turns a blog entry into imports.
CREATE TABLE post_recipes (
    post_id   uuid NOT NULL REFERENCES posts(id)   ON DELETE CASCADE,
    recipe_id uuid NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    position  int  NOT NULL DEFAULT 0,
    PRIMARY KEY (post_id, recipe_id)
);
CREATE INDEX post_recipes_post_idx   ON post_recipes (post_id, position);
CREATE INDEX post_recipes_recipe_idx ON post_recipes (recipe_id);

-- --------------------------------------------------------- derived text --

-- sk_blocks_text flattens the typed blocks into one searchable string.
--
-- IMMUTABLE and free of anything that is not: the same discipline 0001 applies
-- to sk_normalize_key, where unaccent() was rejected for exactly this reason.
--
-- ORDER BY ord is not optional. string_agg without an explicit order is
-- non-deterministic, and this value is STORED -- a table rewrite could
-- silently reshuffle it and move search results with it.
CREATE OR REPLACE FUNCTION sk_blocks_text(blocks jsonb)
RETURNS text
LANGUAGE sql
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT coalesce(string_agg(x.t, ' ' ORDER BY e.ord), '')
      FROM jsonb_array_elements(blocks) WITH ORDINALITY AS e(b, ord)
      CROSS JOIN LATERAL (
          SELECT CASE e.b ->> 'type'
              WHEN 'paragraph' THEN e.b ->> 'text'
              WHEN 'heading'   THEN e.b ->> 'text'
              WHEN 'quote'     THEN e.b ->> 'text'
              WHEN 'list'      THEN (SELECT string_agg(v #>> '{}', ' ')
                                       FROM jsonb_array_elements(e.b -> 'items') v)
              WHEN 'photo'     THEN e.b ->> 'caption'
              WHEN 'gallery'   THEN e.b ->> 'caption'
              WHEN 'video'     THEN e.b ->> 'caption'
              ELSE NULL
          END AS t
      ) x
     WHERE x.t IS NOT NULL
$$;

-- A BEFORE trigger, not the sk_recipe_refresh_derived shape: that one
-- aggregates child rows, while this is a pure function of one column in the
-- same row. Doing it here rather than in Go means an admin tool, a moderation
-- action or a backfill cannot leave the search index describing an older
-- version of the text.
CREATE OR REPLACE FUNCTION sk_posts_body_text()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.body_text := sk_blocks_text(NEW.body_blocks);
    RETURN NEW;
END;
$$;

CREATE TRIGGER posts_body_text BEFORE INSERT OR UPDATE OF body_blocks ON posts
    FOR EACH ROW EXECUTE FUNCTION sk_posts_body_text();

-- ------------------------------------------------- media readiness, again --

-- 0007 added this trigger to repair recipes.has_video. Posts need the same
-- signal: extend the one function rather than add a second trigger on the
-- same table, so the ordering between them can never become a question.
CREATE OR REPLACE FUNCTION sk_media_status_changed()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF (OLD.status = 'ready') IS DISTINCT FROM (NEW.status = 'ready') THEN
        PERFORM sk_recipe_refresh_derived(rm.recipe_id)
           FROM recipe_media rm
          WHERE rm.media_id = NEW.id;

        UPDATE posts SET video_ready = (NEW.status = 'ready')
         WHERE video_media_id = NEW.id;
    END IF;
    RETURN NULL;
END;
$$;

-- ---------------------------------------------------------------- indexes --

-- kind lives in the predicate rather than the key: there are two values and no
-- read path ever wants them mixed.

-- The author's blog tab.
CREATE INDEX posts_author_articles_idx ON posts (author_id, published_at DESC, id DESC)
    WHERE kind = 'article' AND status = 'published' AND deleted_at IS NULL;

-- The author's shorts tab.
CREATE INDEX posts_author_shorts_idx ON posts (author_id, published_at DESC, id DESC)
    WHERE kind = 'short' AND status = 'published' AND deleted_at IS NULL;

-- The global shorts feed, newest first. This is the default: on a young store
-- every score is zero, and ordering by score alone would degenerate to random
-- uuid order and bury new work.
CREATE INDEX posts_shorts_new_idx ON posts (published_at DESC, id DESC)
    WHERE kind = 'short' AND video_ready AND status = 'published' AND deleted_at IS NULL;

-- The global shorts feed, ranked. feed_score is frozen between refreshes, so
-- the keyset comparison stays valid for the length of a paging session.
CREATE INDEX posts_shorts_top_idx ON posts (feed_score DESC, id DESC)
    WHERE kind = 'short' AND video_ready AND status = 'published' AND deleted_at IS NULL;

-- Full text over articles only; a short's caption is not an article body.
CREATE INDEX posts_search_idx ON posts USING gin (search_vector)
    WHERE kind = 'article' AND status = 'published' AND deleted_at IS NULL;

-- The author's own dashboard lists drafts and rejections, which every
-- read-path index above deliberately excludes. Mirrors recipes_author_status_idx.
CREATE INDEX posts_author_status_idx ON posts (author_id, status, updated_at DESC)
    WHERE deleted_at IS NULL;

-- The moderation queue is a small hot slice. Mirrors recipes_review_queue_idx.
CREATE INDEX posts_review_queue_idx ON posts (updated_at ASC)
    WHERE status = 'review' AND deleted_at IS NULL;
