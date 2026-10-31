-- Likes and views for posts and shorts, and one import per person.
--
-- posts.like_count existed from 0008 and fed ranking, but nothing ever wrote
-- it: there was no way to like anything, so the "top" feed ranked by comments
-- alone. post_likes is the event table; the counter is derived by trigger,
-- the same way comment_count and import_count are.

CREATE TABLE post_likes (
    post_id    uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (post_id, user_id)
);
CREATE INDEX post_likes_user_idx ON post_likes (user_id, created_at DESC);

CREATE FUNCTION sk_post_likes_apply() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        UPDATE posts SET like_count = like_count + 1 WHERE id = NEW.post_id;
    ELSE
        -- A cascade from a deleted user or post lands here too.
        UPDATE posts SET like_count = greatest(like_count - 1, 0) WHERE id = OLD.post_id;
    END IF;
    RETURN NULL;
END;
$$;
CREATE TRIGGER post_likes_aggregate AFTER INSERT OR DELETE ON post_likes
    FOR EACH ROW EXECUTE FUNCTION sk_post_likes_apply();

-- A short is watched in a feed, not opened as a page, so the client reports
-- the view itself. A plain counter: per-viewer dedupe is not worth a table
-- while the per-IP rate limit caps the abuse.
ALTER TABLE posts ADD COLUMN view_count bigint NOT NULL DEFAULT 0;

-- Every bundle fetch used to count as an import, so re-opening a recipe or a
-- retried request inflated the store's main metric. One import per signed-in
-- person; anonymous fetches are no longer recorded at all.
DELETE FROM recipe_imports a
 USING recipe_imports b
 WHERE a.user_id = b.user_id AND a.recipe_id = b.recipe_id
   AND a.created_at > b.created_at;
CREATE UNIQUE INDEX recipe_imports_user_recipe_key
    ON recipe_imports (user_id, recipe_id) WHERE user_id IS NOT NULL;
