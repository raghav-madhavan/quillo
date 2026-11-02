-- +goose Up
-- Quillo's addon manager. These tables belong to the Quillo migration set
-- (internal/addons/migrate.go, version table quillo_goose_db_version), never to
-- Silo's migrations/sql, so stock Silo ignores them and upstream merges stay
-- clean.

CREATE TABLE quillo_addons (
    id               bigserial PRIMARY KEY,
    -- The manifest URL as the admin pasted it, normalized to https and to
    -- end in /manifest.json. Configured addons carry their settings in it, so
    -- it can hold credentials and is never shown to non-admins.
    manifest_url     text        NOT NULL,
    -- The addon's own id from the manifest ("com.linvo.cinemeta").
    addon_id         text        NOT NULL,
    name             text        NOT NULL,
    -- The last manifest fetched, as the addon served it.
    manifest         jsonb       NOT NULL,
    enabled          boolean     NOT NULL DEFAULT true,
    -- Lower runs first: catalog rows and stream results follow this order.
    position         integer     NOT NULL,
    -- Both NULL is the server-wide set every profile gets; a profile adds an
    -- addon for that profile only. Profiles are keyed by (user_id, id).
    owner_user_id    bigint,
    owner_profile_id text,
    last_checked_at  timestamptz NOT NULL DEFAULT now(),
    -- Plain-English reason the last manifest refresh failed, or NULL.
    last_error       text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT quillo_addons_owner_pair CHECK ((owner_user_id IS NULL) = (owner_profile_id IS NULL)),
    CONSTRAINT quillo_addons_owner_fk FOREIGN KEY (owner_user_id, owner_profile_id)
        REFERENCES user_profiles (user_id, id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX quillo_addons_owner_url_idx
    ON quillo_addons ((COALESCE(owner_profile_id, '')), manifest_url);

CREATE INDEX quillo_addons_owner_position_idx
    ON quillo_addons ((COALESCE(owner_profile_id, '')), position);

-- Small key/value store for Quillo's own one-time markers, such as whether the
-- starter addons were offered once already.
CREATE TABLE quillo_meta (
    key        text PRIMARY KEY,
    value      text        NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE quillo_meta;
DROP TABLE quillo_addons;
