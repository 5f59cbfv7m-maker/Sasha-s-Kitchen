-- Extensions, shared functions, identity and taxonomy.

CREATE EXTENSION IF NOT EXISTS pgcrypto;   -- gen_random_uuid
CREATE EXTENSION IF NOT EXISTS pg_trgm;    -- fuzzy title search
CREATE EXTENSION IF NOT EXISTS btree_gin;  -- composite GIN with scalar columns
CREATE EXTENSION IF NOT EXISTS citext;     -- case-insensitive handles and emails

-- sk_normalize_key is THE canonical product-name key used to match a recipe's
-- ingredients against a user's pantry. The client must implement byte-identical
-- normalisation; any divergence silently breaks ingredient matching.
--
-- Deliberately IMMUTABLE and free of unaccent(): unaccent is not immutable, so
-- it cannot appear in a generated column or an index expression. Russian only
-- needs ё->е folding, which translate() handles immutably.
--
-- Only ё->е is folded. й->и was considered and rejected: it is not a standard
-- Russian folding and would collide unrelated product names.
CREATE OR REPLACE FUNCTION sk_normalize_key(input text)
RETURNS text
LANGUAGE sql
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT regexp_replace(
               btrim(lower(translate(input, 'Ёё', 'Ее'))),
               '\s+', ' ', 'g')
$$;

-- Touch trigger keeping updated_at honest without relying on the application.
CREATE OR REPLACE FUNCTION sk_touch_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

-- ---------------------------------------------------------------- identity --

CREATE TABLE users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    handle          citext NOT NULL,
    display_name    text   NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 80),
    email           citext,
    password_hash   text,
    bio             text CHECK (bio IS NULL OR length(bio) <= 1000),
    avatar_media_id uuid,
    is_author       boolean NOT NULL DEFAULT false,
    is_admin        boolean NOT NULL DEFAULT false,
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'suspended', 'deleted')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz,
    CONSTRAINT users_handle_format CHECK (handle ~ '^[a-z0-9_]{3,30}$')
);

-- Partial uniqueness: a deleted account frees its handle and email for reuse,
-- which is what account deletion (App Store 5.1.1(v)) is expected to do.
CREATE UNIQUE INDEX users_handle_active_key ON users (handle) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX users_email_active_key  ON users (email)  WHERE deleted_at IS NULL AND email IS NOT NULL;

CREATE TRIGGER users_touch BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION sk_touch_updated_at();

-- Sign in with Apple and any future provider. Kept separate from users so one
-- account can carry several identities.
CREATE TABLE external_identities (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider   text NOT NULL CHECK (provider IN ('apple', 'google', 'vk')),
    subject    text NOT NULL,
    email      citext,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, subject)
);
CREATE INDEX external_identities_user_idx ON external_identities (user_id);

-- Refresh tokens are stored hashed: a database leak must not grant sessions.
CREATE TABLE refresh_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    issued_at  timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    user_agent text,
    ip         inet
);
CREATE INDEX refresh_tokens_user_idx    ON refresh_tokens (user_id) WHERE revoked_at IS NULL;
CREATE INDEX refresh_tokens_expiry_idx  ON refresh_tokens (expires_at) WHERE revoked_at IS NULL;

-- ---------------------------------------------------------------- taxonomy --

CREATE TABLE categories (
    id       smallserial PRIMARY KEY,
    slug     text NOT NULL UNIQUE,
    title    text NOT NULL,
    position smallint NOT NULL DEFAULT 0
);

CREATE TABLE cuisines (
    id       smallserial PRIMARY KEY,
    slug     text NOT NULL UNIQUE,
    title    text NOT NULL,
    position smallint NOT NULL DEFAULT 0
);

CREATE TABLE diet_tags (
    id       smallserial PRIMARY KEY,
    slug     text NOT NULL UNIQUE,
    title    text NOT NULL,
    position smallint NOT NULL DEFAULT 0
);
