-- +goose Up
-- Quillo-made items are filled in by Silo's own metadata providers (cast and
-- crew, collections, trailers, seasons and episodes), the way a scanned file
-- would be. These record when that last succeeded or how often it failed.
ALTER TABLE quillo_items
    ADD COLUMN enriched_at      timestamptz,
    ADD COLUMN enrich_failures  integer NOT NULL DEFAULT 0,
    ADD COLUMN enrich_tried_at  timestamptz;

CREATE INDEX quillo_items_enrich_queue_idx ON quillo_items (last_seen_at DESC)
    WHERE enriched_at IS NULL;

-- +goose Down
DROP INDEX quillo_items_enrich_queue_idx;
ALTER TABLE quillo_items
    DROP COLUMN enrich_tried_at,
    DROP COLUMN enrich_failures,
    DROP COLUMN enriched_at;
