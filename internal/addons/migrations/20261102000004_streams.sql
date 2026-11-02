-- +goose Up
-- Addon streams shown as versions. Each is a media_files row in the
-- Streaming library whose file_path is a placeholder under /quillo-stream/;
-- this table holds where its bytes really are. The URL can carry an addon's
-- or debrid service's credentials, so it is never returned by the API.
CREATE TABLE quillo_streams (
    file_path   text PRIMARY KEY REFERENCES media_files (file_path) ON UPDATE CASCADE ON DELETE CASCADE,
    -- "movie:tt0111161" or "series:tt0903747:1:2": the title the stream plays.
    title_key   text        NOT NULL,
    addon_id    bigint      NOT NULL,
    url         text        NOT NULL,
    -- Request headers the addon asked for (behaviorHints.proxyHeaders.request).
    headers     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- Order the streams were ranked in; lower plays first.
    rank        integer     NOT NULL,
    fetched_at  timestamptz NOT NULL DEFAULT now(),
    probed_at   timestamptz,
    probe_error text
);

CREATE INDEX quillo_streams_title_idx ON quillo_streams (title_key, rank);

-- When each title's streams were last asked for, so every view of a detail
-- page doesn't ask the addons again.
CREATE TABLE quillo_stream_sets (
    title_key  text PRIMARY KEY,
    fetched_at timestamptz NOT NULL DEFAULT now(),
    stream_count integer   NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE quillo_stream_sets;
DROP TABLE quillo_streams;
