-- Editorial storefront, engagement signals, and the UGC safety surface.

-- -------------------------------------------------------------- storefront --

-- Collections are the App Store-style shelves: a hero banner, a horizontal rail
-- of video cards, a grid. Ordering and scheduling live in the database so the
-- storefront can be re-merchandised without shipping a client build.
CREATE TABLE collections (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        text NOT NULL UNIQUE,
    title       text NOT NULL,
    subtitle    text,
    kind        text NOT NULL DEFAULT 'shelf'
                CHECK (kind IN ('hero', 'shelf', 'grid', 'spotlight')),
    position    int  NOT NULL DEFAULT 0,
    is_active   boolean NOT NULL DEFAULT true,
    starts_at   timestamptz,
    ends_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT collections_window CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);
CREATE INDEX collections_active_idx ON collections (position, id) WHERE is_active;
CREATE TRIGGER collections_touch BEFORE UPDATE ON collections
    FOR EACH ROW EXECUTE FUNCTION sk_touch_updated_at();

CREATE TABLE collection_items (
    collection_id uuid NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    recipe_id     uuid NOT NULL REFERENCES recipes(id)     ON DELETE CASCADE,
    position      int  NOT NULL DEFAULT 0,
    PRIMARY KEY (collection_id, recipe_id)
);
CREATE INDEX collection_items_order_idx  ON collection_items (collection_id, position);
CREATE INDEX collection_items_recipe_idx ON collection_items (recipe_id);

-- ------------------------------------------------------------------ social --

CREATE TABLE favorites (
    user_id    uuid NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    recipe_id  uuid NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, recipe_id)
);
CREATE INDEX favorites_recipe_idx ON favorites (recipe_id);
CREATE INDEX favorites_user_recent_idx ON favorites (user_id, created_at DESC);

-- One row per "забрал себе". This is the store's primary success metric, so it
-- is an event table rather than a counter: the counter on recipes is derived.
CREATE TABLE recipe_imports (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid REFERENCES users(id) ON DELETE SET NULL,
    recipe_id  uuid NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    client     text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX recipe_imports_recipe_idx ON recipe_imports (recipe_id, created_at DESC);
CREATE INDEX recipe_imports_user_idx   ON recipe_imports (user_id, created_at DESC);

CREATE TABLE ratings (
    user_id    uuid NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    recipe_id  uuid NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    score      smallint NOT NULL CHECK (score BETWEEN 1 AND 5),
    comment    text CHECK (comment IS NULL OR length(comment) <= 2000),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, recipe_id)
);
CREATE INDEX ratings_recipe_idx ON ratings (recipe_id, created_at DESC);
CREATE TRIGGER ratings_touch BEFORE UPDATE ON ratings
    FOR EACH ROW EXECUTE FUNCTION sk_touch_updated_at();

-- Rating aggregates are kept on recipes by trigger. Recomputing an average over
-- every rating at read time is the classic way to make a storefront slow.
CREATE OR REPLACE FUNCTION sk_ratings_apply()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE recipes SET rating_sum = rating_sum + NEW.score,
                           rating_count = rating_count + 1
         WHERE id = NEW.recipe_id;
    ELSIF TG_OP = 'UPDATE' THEN
        UPDATE recipes SET rating_sum = rating_sum - OLD.score + NEW.score
         WHERE id = NEW.recipe_id;
    ELSE
        UPDATE recipes SET rating_sum = greatest(rating_sum - OLD.score, 0),
                           rating_count = greatest(rating_count - 1, 0)
         WHERE id = OLD.recipe_id;
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER ratings_aggregate AFTER INSERT OR UPDATE OR DELETE ON ratings
    FOR EACH ROW EXECUTE FUNCTION sk_ratings_apply();

CREATE OR REPLACE FUNCTION sk_favorites_apply()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE recipes SET favorite_count = favorite_count + 1 WHERE id = NEW.recipe_id;
    ELSE
        UPDATE recipes SET favorite_count = greatest(favorite_count - 1, 0) WHERE id = OLD.recipe_id;
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER favorites_aggregate AFTER INSERT OR DELETE ON favorites
    FOR EACH ROW EXECUTE FUNCTION sk_favorites_apply();

CREATE OR REPLACE FUNCTION sk_imports_apply()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    UPDATE recipes SET import_count = import_count + 1 WHERE id = NEW.recipe_id;
    RETURN NULL;
END;
$$;
CREATE TRIGGER imports_aggregate AFTER INSERT ON recipe_imports
    FOR EACH ROW EXECUTE FUNCTION sk_imports_apply();

-- ------------------------------------------------ moderation and UGC safety --
--
-- App Store Guideline 1.2 requires a reporting path, the ability to block an
-- author, and a moderator able to take content down. None of this is optional
-- for a user-generated-content app, so it ships with the first schema.

CREATE TABLE reports (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id  uuid REFERENCES users(id) ON DELETE SET NULL,
    target_type  text NOT NULL CHECK (target_type IN ('recipe', 'user', 'rating')),
    target_id    uuid NOT NULL,
    reason       text NOT NULL CHECK (reason IN
                 ('spam', 'offensive', 'copyright', 'unsafe_food', 'sexual', 'other')),
    details      text CHECK (details IS NULL OR length(details) <= 2000),
    status       text NOT NULL DEFAULT 'open'
                 CHECK (status IN ('open', 'reviewing', 'upheld', 'dismissed')),
    resolver_id  uuid REFERENCES users(id) ON DELETE SET NULL,
    resolution   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    resolved_at  timestamptz
);
CREATE INDEX reports_open_idx   ON reports (created_at ASC) WHERE status IN ('open', 'reviewing');
CREATE INDEX reports_target_idx ON reports (target_type, target_id);
-- One open report per reporter per target: a rage-tapping user must not be able
-- to flood the moderation queue.
CREATE UNIQUE INDEX reports_one_open_per_reporter
    ON reports (reporter_id, target_type, target_id)
    WHERE status IN ('open', 'reviewing') AND reporter_id IS NOT NULL;

CREATE TABLE user_blocks (
    blocker_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker_id, blocked_id),
    CONSTRAINT user_blocks_not_self CHECK (blocker_id <> blocked_id)
);
CREATE INDEX user_blocks_blocked_idx ON user_blocks (blocked_id);

CREATE TABLE moderation_events (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    recipe_id  uuid REFERENCES recipes(id) ON DELETE CASCADE,
    user_id    uuid REFERENCES users(id)   ON DELETE SET NULL,
    actor_id   uuid REFERENCES users(id)   ON DELETE SET NULL,
    action     text NOT NULL CHECK (action IN
               ('submitted', 'approved', 'rejected', 'takedown', 'restored',
                'suspended', 'unsuspended')),
    note       text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX moderation_events_recipe_idx ON moderation_events (recipe_id, created_at DESC);
CREATE INDEX moderation_events_actor_idx  ON moderation_events (actor_id, created_at DESC);
