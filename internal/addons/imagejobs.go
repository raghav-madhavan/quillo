package addons

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/metadata"
)

// HotlinkAddonArtwork wraps Silo's image-cache job queue so artwork for addon
// titles is never downloaded: the jobs are dropped and the title keeps serving
// its provider links. Titles with a local file are cached as Silo normally
// does. Cast photos go through a separate queue and are not affected.
func HotlinkAddonArtwork(db *pgxpool.Pool, next metadata.ImageCacheJobEnqueuer) metadata.ImageCacheJobEnqueuer {
	return &hotlinkEnqueuer{db: db, next: next}
}

type hotlinkEnqueuer struct {
	db   *pgxpool.Pool
	next metadata.ImageCacheJobEnqueuer
}

func (h *hotlinkEnqueuer) Enqueue(ctx context.Context, in metadata.EnqueueImageCacheJobInput) error {
	keep := h.filter(ctx, []metadata.EnqueueImageCacheJobInput{in})
	if len(keep) == 0 {
		return nil
	}
	return h.next.Enqueue(ctx, in)
}

func (h *hotlinkEnqueuer) EnqueueBatch(ctx context.Context, inputs []metadata.EnqueueImageCacheJobInput) (int, error) {
	keep := h.filter(ctx, inputs)
	if len(keep) == 0 {
		return 0, nil
	}
	return h.next.EnqueueBatch(ctx, keep)
}

// owner is the title a job's artwork belongs to: the series for season and
// episode artwork, the item itself otherwise.
func owner(in metadata.EnqueueImageCacheJobInput) string {
	if in.SeriesID != "" {
		return in.SeriesID
	}
	return in.TargetContentID
}

func (h *hotlinkEnqueuer) filter(ctx context.Context, inputs []metadata.EnqueueImageCacheJobInput) []metadata.EnqueueImageCacheJobInput {
	ids := make([]string, 0, len(inputs))
	for _, in := range inputs {
		if id := owner(in); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return inputs
	}
	rows, err := h.db.Query(ctx, `
		SELECT q.content_id FROM quillo_items q
		WHERE q.content_id = ANY($1)
		  AND NOT EXISTS (SELECT 1 FROM media_files f WHERE f.content_id = q.content_id)
		  AND NOT EXISTS (SELECT 1 FROM media_files f JOIN episodes e ON e.content_id = f.episode_id
		                  WHERE e.series_id = q.content_id)`, ids)
	if err != nil {
		// When in doubt, cache: Silo's normal behaviour is never wrong, only bigger.
		slog.WarnContext(ctx, "quillo: checking image-cache jobs for addon titles", "error", err)
		return inputs
	}
	skip, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return inputs
	}
	if len(skip) == 0 {
		return inputs
	}
	skipSet := make(map[string]bool, len(skip))
	for _, id := range skip {
		skipSet[id] = true
	}
	keep := inputs[:0:0]
	for _, in := range inputs {
		if !skipSet[owner(in)] {
			keep = append(keep, in)
		}
	}
	return keep
}
