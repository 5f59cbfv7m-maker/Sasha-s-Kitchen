-- Comments on recipes and posts.
--
-- Deliberately NOT polymorphic, unlike reports and moderation_events. Those
-- are append-only admin-scale logs where a missing foreign key costs little.
-- Comments are the opposite on every axis: user-scale volume, a counter on the
-- target, and a hard requirement that deleting a recipe takes its comments
-- with it. reports already shows what polymorphism costs here -- target_id
-- carries no foreign key, so a complaint about a UUID that was never issued
-- used to be accepted, and the error branch written to catch it was
-- unreachable.
--
-- So: an exclusive arc. Two real foreign keys, exactly one of them filled.

ALTER TABLE recipes ADD COLUMN comment_count bigint NOT NULL DEFAULT 0;

CREATE TABLE comments (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    recipe_id uuid REFERENCES recipes(id) ON DELETE CASCADE,
    post_id   uuid REFERENCES posts(id)   ON DELETE CASCADE,
    author_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    parent_id uuid REFERENCES comments(id) ON DELETE CASCADE,
    -- One level of replies. Deeper threads need collapsing, renderers for
    -- arbitrary nesting and a moderation story for buried branches; none of
    -- that is worth it under a recipe.
    depth     smallint NOT NULL DEFAULT 0 CHECK (depth IN (0, 1)),
    -- Always 0, and referenced by the composite foreign key below to force any
    -- parent to be a root. A constant column is a strange thing to store, but
    -- it buys the rule declaratively instead of in a trigger.
    parent_depth smallint NOT NULL DEFAULT 0 CHECK (parent_depth = 0),

    body   text NOT NULL CHECK (length(btrim(body)) BETWEEN 1 AND 2000),
    status text NOT NULL DEFAULT 'visible'
           CHECK (status IN ('visible', 'hidden', 'removed')),

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT comments_one_target CHECK (num_nonnulls(recipe_id, post_id) = 1),
    CONSTRAINT comments_depth_matches_parent CHECK ((parent_id IS NULL) = (depth = 0)),

    -- Targets for the composite foreign keys below. Redundant against the
    -- primary key, but a foreign key needs a unique constraint to point at.
    UNIQUE (id, recipe_id),
    UNIQUE (id, post_id),
    UNIQUE (id, depth),

    -- A reply must hang off a comment on the SAME target, and that comment must
    -- itself be a root. MATCH SIMPLE skips a composite key when any column is
    -- NULL, so the recipe arm is inert on a post comment and vice versa, and
    -- both are inert on a root comment. Between them these three keys enforce
    -- in the database what would otherwise be three checks in Go.
    FOREIGN KEY (parent_id, recipe_id)    REFERENCES comments (id, recipe_id) ON DELETE CASCADE,
    FOREIGN KEY (parent_id, post_id)      REFERENCES comments (id, post_id)   ON DELETE CASCADE,
    FOREIGN KEY (parent_id, parent_depth) REFERENCES comments (id, depth)
);

CREATE TRIGGER comments_touch BEFORE UPDATE ON comments
    FOR EACH ROW EXECUTE FUNCTION sk_touch_updated_at();

-- Root comments are paged; replies to a page of roots are fetched in one go.
-- You cannot keyset-paginate rows in a nested structure, so only the roots
-- carry a cursor.
--
-- The predicates say status = 'visible' verbatim. Writing status <> 'removed'
-- in a query instead would stop the planner using these indexes at all.
CREATE INDEX comments_recipe_roots_idx ON comments (recipe_id, created_at DESC, id DESC)
    WHERE parent_id IS NULL AND status = 'visible';
CREATE INDEX comments_post_roots_idx ON comments (post_id, created_at DESC, id DESC)
    WHERE parent_id IS NULL AND status = 'visible';
-- Replies read in chronological order: ASC/ASC, same direction as each other,
-- which is what the row-wise comparison needs.
CREATE INDEX comments_replies_idx ON comments (parent_id, created_at ASC, id ASC)
    WHERE status = 'visible';
-- A moderator looking at one person's history, and the rate limiter.
CREATE INDEX comments_author_idx ON comments (author_id, created_at DESC);

-- --------------------------------------------------------------- counters --

-- The counter must track exactly the rows the read path shows, or the UI
-- renders "12 комментариев" above nine of them. It therefore keys off
-- status = 'visible' on insert, delete AND status change.
CREATE OR REPLACE FUNCTION sk_comments_apply()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    delta int := 0;
    rec   uuid;
    pst   uuid;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.status = 'visible' THEN delta := 1; END IF;
        rec := NEW.recipe_id; pst := NEW.post_id;
    ELSIF TG_OP = 'DELETE' THEN
        IF OLD.status = 'visible' THEN delta := -1; END IF;
        rec := OLD.recipe_id; pst := OLD.post_id;
    ELSE
        IF NEW.status = 'visible' AND OLD.status <> 'visible' THEN
            delta := 1;
        ELSIF NEW.status <> 'visible' AND OLD.status = 'visible' THEN
            delta := -1;
        END IF;
        rec := NEW.recipe_id; pst := NEW.post_id;
    END IF;

    IF delta <> 0 THEN
        IF rec IS NOT NULL THEN
            UPDATE recipes SET comment_count = greatest(comment_count + delta, 0) WHERE id = rec;
        ELSE
            UPDATE posts SET comment_count = greatest(comment_count + delta, 0) WHERE id = pst;
        END IF;
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER comments_apply AFTER INSERT OR UPDATE OR DELETE ON comments
    FOR EACH ROW EXECUTE FUNCTION sk_comments_apply();

-- ------------------------------------------------------- self-rating gate --

-- Unrelated to comments, but the same class of hole and cheap to close while
-- nothing writes ratings yet: an author must not be able to rate their own
-- recipe, or the leaderboard's rating term is self-serve.
CREATE OR REPLACE FUNCTION sk_ratings_not_self()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM recipes r
                WHERE r.id = NEW.recipe_id AND r.author_id = NEW.user_id) THEN
        RAISE EXCEPTION 'an author cannot rate their own recipe'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER ratings_not_self BEFORE INSERT OR UPDATE ON ratings
    FOR EACH ROW EXECUTE FUNCTION sk_ratings_not_self();
