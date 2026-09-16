-- Refresh-token families.
--
-- Rotation alone is not enough: if a stolen token is replayed after the victim
-- has already rotated, the server must be able to tell that the whole chain is
-- compromised. A family id groups every token descended from one login, so a
-- replay revokes exactly that chain instead of signing the user out of every
-- device they own.
ALTER TABLE refresh_tokens
    ADD COLUMN family_id uuid NOT NULL DEFAULT gen_random_uuid(),
    ADD COLUMN parent_id uuid REFERENCES refresh_tokens(id) ON DELETE SET NULL;

CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id)
    WHERE revoked_at IS NULL;
