package addons

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

// The Streaming library holds addon titles once Silo's metadata providers
// have filled them in. Silo only lists seasons and episodes, and only tracks
// Continue Watching and Next Up, for titles in a library, so membership is
// what makes addon series and movies behave like library titles in the apps.
// It is a virtual library: it has no folders, and catalog.IsVirtualLibrary
// keeps scans and membership reconciliation away from it.
const (
	streamingLibraryKey  = "streaming_library_id"
	streamingLibraryName = "Streaming"
	sweepEvery           = 10 * time.Minute
)

// streamingJoinedAt is the "added" date of addon titles and their Streaming
// memberships. Recently Added rows sort by these dates, so addon titles sort
// after everything from a real library instead of flooding those rows.
var streamingJoinedAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// ensureStreamingLibrary finds or creates the Streaming library and registers
// it as virtual.
func (s *Service) ensureStreamingLibrary(ctx context.Context) (int, error) {
	if v, ok, err := s.store.Meta(ctx, streamingLibraryKey); err != nil {
		return 0, err
	} else if ok {
		id, _ := strconv.Atoi(v)
		var exists bool
		if err := s.store.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media_folders WHERE id = $1)`, id).Scan(&exists); err != nil {
			return 0, err
		}
		if exists {
			catalog.RegisterVirtualLibrary(id)
			return id, nil
		}
		// An admin deleted it; make a new one.
	}
	var id int
	err := s.store.db.QueryRow(ctx, `INSERT INTO media_folders (type, name, enabled, realtime_monitoring, sort_order)
		VALUES ('mixed', $1, true, false, 9999) RETURNING id`, streamingLibraryName).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("creating the Streaming library: %w", err)
	}
	catalog.RegisterVirtualLibrary(id)
	if err := s.store.SetMeta(ctx, streamingLibraryKey, strconv.Itoa(id)); err != nil {
		return 0, err
	}
	slog.InfoContext(ctx, "quillo created the Streaming library", "library_id", id)
	return id, nil
}

// afterEnrich runs once Silo's providers filled an addon title in: its
// artwork goes back to provider links and it joins the Streaming library.
func (s *Service) afterEnrich(ctx context.Context, contentID string) {
	if err := s.hotlinkArtwork(ctx, contentID); err != nil {
		slog.WarnContext(ctx, "quillo: switching artwork to provider links", "content_id", contentID, "error", err)
	}
	if err := s.joinStreaming(ctx, contentID); err != nil {
		slog.WarnContext(ctx, "quillo: adding to the Streaming library", "content_id", contentID, "error", err)
	}
}

func (s *Service) joinStreaming(ctx context.Context, contentID string) error {
	if s.streamingLibrary == 0 {
		return errors.New("no Streaming library")
	}
	return pgx.BeginFunc(ctx, s.store.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO media_item_libraries (content_id, media_folder_id, first_seen_at)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, contentID, s.streamingLibrary, streamingJoinedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO episode_libraries (episode_id, media_folder_id, first_seen_at)
			SELECT content_id, $2, $3 FROM episodes WHERE series_id = $1
			ON CONFLICT DO NOTHING`, contentID, s.streamingLibrary, streamingJoinedAt)
		if err != nil {
			return err
		}
		// Recently Added rows sort movies by created_at and series by their
		// newest episode; date both before anything from a real library.
		if _, err := tx.Exec(ctx, `UPDATE media_items SET created_at = $2
			WHERE content_id = $1 AND created_at > $2`, contentID, streamingJoinedAt); err != nil {
			return err
		}
		return catalog.RecomputeSeriesEpisodeMembershipDenorms(ctx, tx, []string{contentID})
	})
}

// hotlinkArtwork keeps an addon title's artwork as provider links rather than
// copies on the server's disk: queued downloads are dropped, and slots that
// already serve a stored copy serve the provider source again. Silo's artwork
// revision cleanup deletes the displaced copies. Cast photos are shared with
// the library and stay cached.
func (s *Service) hotlinkArtwork(ctx context.Context, contentID string) error {
	return pgx.BeginFunc(ctx, s.store.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM metadata_image_cache_jobs
			WHERE status = 'queued' AND (target_content_id = $1 OR series_id = $1)`, contentID); err != nil {
			return err
		}
		for _, col := range []string{"poster", "backdrop", "logo"} {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE media_items SET %[1]s_path = %[1]s_source_path
				WHERE content_id = $1 AND %[1]s_source_path LIKE '%%://%%' AND %[1]s_path NOT LIKE '%%://%%'`, col), contentID); err != nil {
				return err
			}
		}
		for _, col := range []string{"poster", "backdrop"} {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`UPDATE seasons SET %[1]s_path = %[1]s_source_path
				WHERE series_id = $1 AND %[1]s_source_path LIKE '%%://%%' AND %[1]s_path NOT LIKE '%%://%%'`, col), contentID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `UPDATE episodes SET still_path = still_source_path
			WHERE series_id = $1 AND still_source_path LIKE '%://%' AND still_path NOT LIKE '%://%'`, contentID)
		return err
	})
}

// sweep catches up on work that Silo itself redoes in the background: a
// scheduled metadata refresh queues artwork downloads again and adds new
// episodes of running series. Titles with a local file are left alone; they
// belong to a real library and keep Silo's normal behaviour.
func (s *Service) sweep(ctx context.Context) {
	rows, err := s.store.db.Query(ctx, `
		SELECT q.content_id FROM quillo_items q
		WHERE q.enriched_at IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM media_files f WHERE f.content_id = q.content_id)
		  AND NOT EXISTS (SELECT 1 FROM media_files f JOIN episodes e ON e.content_id = f.episode_id
		                  WHERE e.series_id = q.content_id)`)
	if err != nil {
		slog.WarnContext(ctx, "quillo sweep: listing titles", "error", err)
		return
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		slog.WarnContext(ctx, "quillo sweep: listing titles", "error", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		s.afterEnrich(ctx, id)
	}
}

func (s *Service) runSweep(ctx context.Context) {
	for {
		s.sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(sweepEvery):
		}
	}
}
