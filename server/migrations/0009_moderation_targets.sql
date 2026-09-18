-- Moderation now covers posts as well as recipes, and publication quotas need
-- to know who published what.

-- ------------------------------------------------------- reports on posts --

-- Constraint name verified against pg_constraint rather than assumed; the
-- default naming happens to hold here, but guessing it is how a migration
-- fails on someone else's database.
ALTER TABLE reports DROP CONSTRAINT reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('recipe', 'user', 'rating', 'post', 'comment'));

-- --------------------------------------------- moderation_events, widened --

-- This table stays polymorphic, unlike comments, and the difference is
-- deliberate. It is an append-only audit log: no counters hang off it and
-- nothing cascades from it, so the integrity a real foreign key would buy has
-- no work to do here. Adding one column per content type instead would mean
-- editing this table forever.
--
-- The log also outlives what it describes now. recipe_id cascaded, so deleting
-- a recipe erased the record of it having been taken down -- exactly the
-- history a moderator needs later.
ALTER TABLE moderation_events
    ADD COLUMN target_type text,
    ADD COLUMN target_id   uuid;

UPDATE moderation_events
   SET target_type = CASE WHEN recipe_id IS NOT NULL THEN 'recipe' ELSE 'user' END,
       target_id   = coalesce(recipe_id, user_id)
 WHERE target_type IS NULL;

-- Rows with neither a recipe nor a user carry nothing to point at; there are
-- none in practice, but the column has to be NOT NULL for the index to be
-- useful, so drop any stragglers first.
DELETE FROM moderation_events WHERE target_id IS NULL;

ALTER TABLE moderation_events
    ALTER COLUMN target_type SET NOT NULL,
    ALTER COLUMN target_id   SET NOT NULL,
    ADD CONSTRAINT moderation_events_target_type_check
        CHECK (target_type IN ('recipe', 'user', 'post', 'comment'));

DROP INDEX IF EXISTS moderation_events_recipe_idx;
ALTER TABLE moderation_events DROP COLUMN recipe_id;

CREATE INDEX moderation_events_target_idx
    ON moderation_events (target_type, target_id, created_at DESC);

-- --------------------------------------------------- quota counting index --

-- Quotas are counted on transitions, not on rows.
--
-- Counting rows in recipes would undercount: a draft sent to review, pulled
-- back and sent again is one row and two publications. moderation_events
-- already records each 'submitted', so the count belongs here -- but user_id
-- was never populated, only actor_id, so the author was not on the row at all
-- and the count was impossible. The Go side now fills it in.
CREATE INDEX moderation_events_author_day_idx
    ON moderation_events (user_id, created_at DESC)
    WHERE action = 'submitted';
