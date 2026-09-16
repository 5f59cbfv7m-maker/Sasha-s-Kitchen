-- Media, recipes, ingredients, derived search columns and the filter indexes.

-- ------------------------------------------------------------------- media --

CREATE TABLE media_assets (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id      uuid REFERENCES users(id) ON DELETE SET NULL,
    kind          text NOT NULL CHECK (kind IN ('photo', 'video')),
    status        text NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'uploaded', 'processing', 'ready', 'failed')),
    storage_key   text NOT NULL,
    content_type  text,
    bytes         bigint CHECK (bytes IS NULL OR bytes >= 0),
    width         int,
    height        int,
    duration_ms   int,
    -- Video derivatives. The API never serves originals; clients get the HLS
    -- manifest and a poster frame, both fronted by the CDN.
    poster_key    text,
    hls_key       text,
    blurhash      text,
    error         text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    processed_at  timestamptz
);
CREATE INDEX media_assets_owner_idx  ON media_assets (owner_id, created_at DESC);
CREATE INDEX media_assets_status_idx ON media_assets (status) WHERE status <> 'ready';

ALTER TABLE users
    ADD CONSTRAINT users_avatar_fk FOREIGN KEY (avatar_media_id)
    REFERENCES media_assets(id) ON DELETE SET NULL;

-- ----------------------------------------------------------------- recipes --

CREATE TABLE recipes (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slug              text NOT NULL,
    title             text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 140),
    summary           text CHECK (summary IS NULL OR length(summary) <= 400),
    description       text,
    steps             jsonb NOT NULL DEFAULT '[]'::jsonb,

    category_id       smallint REFERENCES categories(id) ON DELETE SET NULL,
    cuisine_id        smallint REFERENCES cuisines(id)   ON DELETE SET NULL,
    difficulty        smallint NOT NULL DEFAULT 1 CHECK (difficulty BETWEEN 1 AND 3),
    cook_time_minutes int NOT NULL DEFAULT 0 CHECK (cook_time_minutes BETWEEN 0 AND 6000),
    prep_time_minutes int NOT NULL DEFAULT 0 CHECK (prep_time_minutes BETWEEN 0 AND 6000),
    base_servings     int NOT NULL DEFAULT 2 CHECK (base_servings BETWEEN 1 AND 100),

    -- Paid listings are modelled now so introducing payments later is a code
    -- change, not a migration of live rows.
    access_tier       text NOT NULL DEFAULT 'free' CHECK (access_tier IN ('free', 'paid')),
    price_minor       int CHECK (price_minor IS NULL OR price_minor >= 0),
    currency          char(3),

    status            text NOT NULL DEFAULT 'draft'
                      CHECK (status IN ('draft', 'review', 'published', 'rejected', 'archived')),
    published_at      timestamptz,
    rejected_reason   text,

    -- Derived columns, maintained by sk_recipe_refresh_derived(). They exist so
    -- the storefront never joins recipe_ingredients at query time.
    ingredient_keys          text[] NOT NULL DEFAULT '{}',
    required_ingredient_keys text[] NOT NULL DEFAULT '{}',
    required_count           int    NOT NULL DEFAULT 0,
    ingredients_text         text   NOT NULL DEFAULT '',
    diet_slugs               text[] NOT NULL DEFAULT '{}',
    has_video                boolean NOT NULL DEFAULT false,

    kcal_per_serving    numeric(10,2) NOT NULL DEFAULT 0,
    protein_per_serving numeric(10,2) NOT NULL DEFAULT 0,
    fat_per_serving     numeric(10,2) NOT NULL DEFAULT 0,
    carbs_per_serving   numeric(10,2) NOT NULL DEFAULT 0,

    import_count   bigint NOT NULL DEFAULT 0,
    view_count     bigint NOT NULL DEFAULT 0,
    favorite_count bigint NOT NULL DEFAULT 0,
    rating_sum     bigint NOT NULL DEFAULT 0,
    rating_count   int    NOT NULL DEFAULT 0,
    rating_avg     numeric(3,2) GENERATED ALWAYS AS (
                       CASE WHEN rating_count > 0
                            THEN round(rating_sum::numeric / rating_count, 2)
                            ELSE NULL END
                   ) STORED,

    search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('russian', coalesce(title, '')),            'A') ||
        setweight(to_tsvector('russian', coalesce(summary, '')),          'B') ||
        setweight(to_tsvector('russian', coalesce(ingredients_text, '')), 'C') ||
        setweight(to_tsvector('russian', coalesce(description, '')),      'D')
    ) STORED,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,

    -- A paid listing without a price is a broken storefront card, so the
    -- database refuses it outright.
    CONSTRAINT recipes_paid_needs_price CHECK (
        access_tier <> 'paid' OR (price_minor IS NOT NULL AND currency IS NOT NULL)
    ),
    CONSTRAINT recipes_published_needs_time CHECK (
        status <> 'published' OR published_at IS NOT NULL
    )
);

CREATE UNIQUE INDEX recipes_slug_key ON recipes (slug) WHERE deleted_at IS NULL;
CREATE TRIGGER recipes_touch BEFORE UPDATE ON recipes
    FOR EACH ROW EXECUTE FUNCTION sk_touch_updated_at();

-- Ingredients carry the FULL product definition, not a reference to a shared
-- catalogue. That is what lets an importing client create products it has never
-- seen without a second round trip, and it freezes the author's numbers so a
-- later edit elsewhere cannot silently restate someone's published recipe.
CREATE TABLE recipe_ingredients (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    recipe_id     uuid NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    position      int  NOT NULL DEFAULT 0,

    product_name  text NOT NULL CHECK (length(btrim(product_name)) BETWEEN 1 AND 120),
    product_key   text GENERATED ALWAYS AS (sk_normalize_key(product_name)) STORED,

    -- amount_per_base_serving is the amount for the WHOLE recipe at
    -- base_servings, matching the client's RecipeIngredient semantics.
    amount_per_base_serving numeric(12,3) NOT NULL CHECK (amount_per_base_serving > 0),
    unit                    text NOT NULL CHECK (unit IN ('г', 'мл', 'шт')),
    grams_per_unit          numeric(10,3) NOT NULL DEFAULT 1 CHECK (grams_per_unit > 0),

    category            text,
    kcal_per_100        numeric(10,3) NOT NULL DEFAULT 0 CHECK (kcal_per_100 >= 0),
    protein_per_100     numeric(10,3) NOT NULL DEFAULT 0 CHECK (protein_per_100 >= 0),
    fat_per_100         numeric(10,3) NOT NULL DEFAULT 0 CHECK (fat_per_100 >= 0),
    carbs_per_100       numeric(10,3) NOT NULL DEFAULT 0 CHECK (carbs_per_100 >= 0),
    shelf_life_days     int CHECK (shelf_life_days IS NULL OR shelf_life_days > 0),
    low_stock_threshold numeric(12,3) NOT NULL DEFAULT 0 CHECK (low_stock_threshold >= 0),

    is_optional boolean NOT NULL DEFAULT false,
    note        text,

    UNIQUE (recipe_id, product_key)
);
CREATE INDEX recipe_ingredients_recipe_idx ON recipe_ingredients (recipe_id, position);
CREATE INDEX recipe_ingredients_key_idx    ON recipe_ingredients (product_key);

CREATE TABLE recipe_diets (
    recipe_id   uuid     NOT NULL REFERENCES recipes(id)   ON DELETE CASCADE,
    diet_tag_id smallint NOT NULL REFERENCES diet_tags(id) ON DELETE CASCADE,
    PRIMARY KEY (recipe_id, diet_tag_id)
);

CREATE TABLE recipe_media (
    recipe_id uuid NOT NULL REFERENCES recipes(id)      ON DELETE CASCADE,
    media_id  uuid NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    position  int  NOT NULL DEFAULT 0,
    role      text NOT NULL DEFAULT 'gallery' CHECK (role IN ('hero', 'gallery', 'step')),
    PRIMARY KEY (recipe_id, media_id)
);
CREATE INDEX recipe_media_recipe_idx ON recipe_media (recipe_id, position);
CREATE INDEX recipe_media_media_idx  ON recipe_media (media_id);

-- ------------------------------------------------- derived-column refresher --

-- Recomputes every denormalised column on one recipe. Called by triggers on the
-- child tables; also safe to call directly after a bulk import.
--
-- Nutrition counts only non-optional ingredients: that is what you actually get
-- by following the base recipe. ingredient_keys, by contrast, includes optional
-- ones, because an allergen hidden in an optional ingredient must still be
-- catchable by an exclusion filter.
CREATE OR REPLACE FUNCTION sk_recipe_refresh_derived(p_recipe_id uuid)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    v_base int;
BEGIN
    SELECT greatest(coalesce(base_servings, 1), 1) INTO v_base
      FROM recipes WHERE id = p_recipe_id;
    IF v_base IS NULL THEN
        RETURN;  -- recipe row is gone; nothing to refresh
    END IF;

    UPDATE recipes r SET
        ingredient_keys          = coalesce(agg.all_keys, '{}'),
        required_ingredient_keys = coalesce(agg.req_keys, '{}'),
        required_count           = coalesce(array_length(agg.req_keys, 1), 0),
        ingredients_text         = coalesce(agg.names, ''),
        kcal_per_serving         = round(coalesce(agg.kcal,    0) / v_base, 2),
        protein_per_serving      = round(coalesce(agg.protein, 0) / v_base, 2),
        fat_per_serving          = round(coalesce(agg.fat,     0) / v_base, 2),
        carbs_per_serving        = round(coalesce(agg.carbs,   0) / v_base, 2)
    FROM (
        SELECT
            array_agg(DISTINCT product_key) AS all_keys,
            array_agg(DISTINCT product_key) FILTER (WHERE NOT is_optional) AS req_keys,
            string_agg(product_name, ' ')   AS names,
            sum(amount_per_base_serving * grams_per_unit * kcal_per_100    / 100.0)
                FILTER (WHERE NOT is_optional) AS kcal,
            sum(amount_per_base_serving * grams_per_unit * protein_per_100 / 100.0)
                FILTER (WHERE NOT is_optional) AS protein,
            sum(amount_per_base_serving * grams_per_unit * fat_per_100     / 100.0)
                FILTER (WHERE NOT is_optional) AS fat,
            sum(amount_per_base_serving * grams_per_unit * carbs_per_100   / 100.0)
                FILTER (WHERE NOT is_optional) AS carbs
        FROM recipe_ingredients
        WHERE recipe_id = p_recipe_id
    ) agg
    WHERE r.id = p_recipe_id;

    UPDATE recipes r SET
        diet_slugs = coalesce((
            SELECT array_agg(d.slug ORDER BY d.slug)
              FROM recipe_diets rd JOIN diet_tags d ON d.id = rd.diet_tag_id
             WHERE rd.recipe_id = p_recipe_id), '{}'),
        has_video = EXISTS (
            SELECT 1 FROM recipe_media rm JOIN media_assets m ON m.id = rm.media_id
             WHERE rm.recipe_id = p_recipe_id AND m.kind = 'video' AND m.status = 'ready')
    WHERE r.id = p_recipe_id;
END;
$$;

CREATE OR REPLACE FUNCTION sk_recipe_child_changed()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM sk_recipe_refresh_derived(COALESCE(NEW.recipe_id, OLD.recipe_id));
    RETURN NULL;
END;
$$;

CREATE TRIGGER recipe_ingredients_refresh
    AFTER INSERT OR UPDATE OR DELETE ON recipe_ingredients
    FOR EACH ROW EXECUTE FUNCTION sk_recipe_child_changed();

CREATE TRIGGER recipe_diets_refresh
    AFTER INSERT OR UPDATE OR DELETE ON recipe_diets
    FOR EACH ROW EXECUTE FUNCTION sk_recipe_child_changed();

CREATE TRIGGER recipe_media_refresh
    AFTER INSERT OR UPDATE OR DELETE ON recipe_media
    FOR EACH ROW EXECUTE FUNCTION sk_recipe_child_changed();

-- --------------------------------------------------------------- indexing ---
--
-- Every storefront query carries "status = 'published' AND deleted_at IS NULL",
-- so all read-path indexes are partial on exactly that predicate. It keeps them
-- a fraction of the table size and out of the write path for drafts.

CREATE INDEX recipes_feed_new_idx ON recipes (published_at DESC, id DESC)
    WHERE status = 'published' AND deleted_at IS NULL;

CREATE INDEX recipes_feed_popular_idx ON recipes (import_count DESC, id DESC)
    WHERE status = 'published' AND deleted_at IS NULL;

-- Keyset pagination compares (sort_key, id) as a row, so both columns must sort
-- in the SAME direction or the comparison stops matching the index.
-- "Top rated" ranks only recipes that actually have ratings, which also keeps
-- rating_avg non-NULL and the cursor comparison total.
CREATE INDEX recipes_feed_rating_idx ON recipes (rating_avg DESC, id DESC)
    WHERE status = 'published' AND deleted_at IS NULL AND rating_count > 0;

CREATE INDEX recipes_feed_quick_idx ON recipes (cook_time_minutes ASC, id ASC)
    WHERE status = 'published' AND deleted_at IS NULL;

CREATE INDEX recipes_feed_light_idx ON recipes (kcal_per_serving ASC, id ASC)
    WHERE status = 'published' AND deleted_at IS NULL;

CREATE INDEX recipes_search_idx ON recipes USING gin (search_vector)
    WHERE status = 'published' AND deleted_at IS NULL;

CREATE INDEX recipes_title_trgm_idx ON recipes USING gin (title gin_trgm_ops)
    WHERE status = 'published' AND deleted_at IS NULL;

-- Include/exclude ingredient filters use @> and &&, which GIN answers directly.
CREATE INDEX recipes_ingredient_keys_idx ON recipes USING gin (ingredient_keys)
    WHERE status = 'published' AND deleted_at IS NULL;

-- "Can cook now" is required_ingredient_keys <@ :pantry. GIN supports <@ but
-- cannot drive it efficiently on its own, so the query also carries
-- required_count <= cardinality(:pantry) to let this btree cut the candidate
-- set first. See docs/PERFORMANCE.md before changing either side.
CREATE INDEX recipes_required_keys_idx ON recipes USING gin (required_ingredient_keys)
    WHERE status = 'published' AND deleted_at IS NULL;
CREATE INDEX recipes_required_count_idx ON recipes (required_count)
    WHERE status = 'published' AND deleted_at IS NULL;

CREATE INDEX recipes_diets_idx ON recipes USING gin (diet_slugs)
    WHERE status = 'published' AND deleted_at IS NULL;

-- btree_gin lets one index answer any combination of the scalar facets.
CREATE INDEX recipes_facets_idx ON recipes
    USING gin (category_id, cuisine_id, difficulty, access_tier, has_video)
    WHERE status = 'published' AND deleted_at IS NULL;

CREATE INDEX recipes_kcal_idx ON recipes (kcal_per_serving)
    WHERE status = 'published' AND deleted_at IS NULL;

CREATE INDEX recipes_author_pub_idx ON recipes (author_id, published_at DESC)
    WHERE deleted_at IS NULL;

-- Author's own dashboard lists drafts and rejections, which the partial
-- read-path indexes above deliberately exclude.
CREATE INDEX recipes_author_status_idx ON recipes (author_id, status, updated_at DESC)
    WHERE deleted_at IS NULL;

-- The moderation queue is a small, hot slice; keep it on its own index.
CREATE INDEX recipes_review_queue_idx ON recipes (updated_at ASC)
    WHERE status = 'review' AND deleted_at IS NULL;
