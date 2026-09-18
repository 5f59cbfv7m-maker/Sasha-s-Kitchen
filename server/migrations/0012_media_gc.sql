-- Garbage collection for media.
--
-- Nothing in this service has ever deleted an object from storage. With
-- articles carrying galleries and shorts carrying a source video plus an HLS
-- ladder and a poster, storage becomes the dominant running cost within
-- months, and every byte of it is paid for forever.
--
-- What is expensive to add later is not the job -- it is the answer to "is
-- anything still pointing at this asset". Once there are millions of rows and
-- no recorded referrer set, that question stops being cheap. So the referrer
-- set is written down here, while it is still six places.

ALTER TABLE media_assets DROP CONSTRAINT media_assets_status_check;
ALTER TABLE media_assets ADD CONSTRAINT media_assets_status_check
    CHECK (status IN ('pending', 'uploaded', 'processing', 'ready', 'failed', 'orphaned'));

CREATE INDEX media_assets_orphaned_idx ON media_assets (created_at)
    WHERE status = 'orphaned';

-- sk_media_unreferenced lists assets nothing points at any more.
--
-- The grace period is load-bearing. An asset is created 'pending' and only
-- attached to a post or a recipe once the client has uploaded it, so a sweep
-- with no grace would delete uploads that are still in flight.
CREATE OR REPLACE FUNCTION sk_media_unreferenced(grace interval)
RETURNS TABLE (id uuid, storage_key text, poster_key text, hls_key text)
LANGUAGE sql
STABLE
AS $$
    SELECT m.id, m.storage_key, m.poster_key, m.hls_key
      FROM media_assets m
     WHERE m.created_at < now() - grace
       AND m.status <> 'orphaned'
       -- The complete referrer set. Adding a seventh place that can hold a
       -- media id means adding it here, or the collector will delete a file
       -- that is still on someone's page.
       AND NOT EXISTS (SELECT 1 FROM recipe_media rm WHERE rm.media_id = m.id)
       AND NOT EXISTS (SELECT 1 FROM post_media pm  WHERE pm.media_id = m.id)
       AND NOT EXISTS (SELECT 1 FROM posts p
                        WHERE p.cover_media_id = m.id OR p.video_media_id = m.id)
       AND NOT EXISTS (SELECT 1 FROM users u
                        WHERE u.avatar_media_id = m.id OR u.cover_media_id = m.id)
       -- A body block can reference an asset that was never added to
       -- post_media, so the stored JSON is part of the referrer set too.
       AND NOT EXISTS (
           SELECT 1 FROM posts p, jsonb_array_elements(p.body_blocks) AS b
            WHERE p.deleted_at IS NULL
              AND (b ->> 'media_id' = m.id::text
                   OR b -> 'media_ids' ? m.id::text))
$$;
