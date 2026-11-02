-- +goose Up
-- Catalog items Quillo created from addon metadata. A row here marks a
-- media_items row as Quillo-made: Quillo may refresh its metadata and, once
-- nobody has used it for a while, delete it. Items that also exist in a
-- library were made by Silo and never get a row here.
CREATE TABLE quillo_items (
    content_id   text PRIMARY KEY REFERENCES media_items (content_id) ON UPDATE CASCADE ON DELETE CASCADE,
    -- The addon-side id and type the item was made from ("tt0903747", "series").
    source_id    text        NOT NULL,
    source_type  text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    -- Last time a row, search or detail page showed the item.
    last_seen_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX quillo_items_last_seen_idx ON quillo_items (last_seen_at);

-- Whether the addon's catalogs were added as home rows. Set once, so rows an
-- admin later deletes from Home Sections stay deleted.
ALTER TABLE quillo_addons ADD COLUMN home_rows_created boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE quillo_addons DROP COLUMN home_rows_created;
DROP TABLE quillo_items;
