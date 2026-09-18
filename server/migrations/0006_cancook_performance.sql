-- Make "могу приготовить сейчас" fast.
--
-- The original formulation was `required_ingredient_keys <@ :pantry` over a GIN
-- index. Measured on 50k recipes / 248k ingredients with a realistic 60-item
-- pantry, that took 275 ms: GIN cannot drive "contained by", so it scanned
-- 156k index entries and rechecked 33k heap rows to return 9 results.
--
-- The replacement is relational division driven from the pantry side: count how
-- many of a recipe's required ingredients appear in the pantry and keep the
-- recipes where that count equals required_count. Same answer, 48 ms, and every
-- step is an index-only scan. See docs/PERFORMANCE.md for the measurements.

-- Lets the counting join read only ingredient rows whose product is in the
-- pantry, without touching the table.
CREATE INDEX IF NOT EXISTS recipe_ingredients_required_key_idx
    ON recipe_ingredients (product_key, recipe_id)
    WHERE NOT is_optional;

-- Narrow covering index so joining the counts back to recipes stays an
-- index-only scan instead of a sequential scan over the wide recipes table.
CREATE INDEX IF NOT EXISTS recipes_cancook_probe_idx
    ON recipes (id, required_count) INCLUDE (published_at)
    WHERE status = 'published' AND deleted_at IS NULL;

-- No query uses this any more, and an unused GIN index is pure write-amplification
-- on every recipe edit. The column itself stays: it is cheap and useful when
-- debugging why a recipe did or did not match a pantry.
DROP INDEX IF EXISTS recipes_required_keys_idx;
