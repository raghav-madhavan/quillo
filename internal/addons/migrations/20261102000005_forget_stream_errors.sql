-- +goose Up
-- Earlier builds stored ffprobe errors that quoted stream URLs, which can
-- carry a debrid token. Streams are no longer opened ahead of playback, so the
-- column stays empty from now on.
UPDATE quillo_streams SET probe_error = NULL, probed_at = NULL;

-- +goose Down
SELECT 1;
