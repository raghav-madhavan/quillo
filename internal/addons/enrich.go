package addons

import (
	"context"
	"log/slog"
	"time"
)

// ItemRefresher is the part of Silo's metadata service Quillo uses to fill in
// an item from its provider ids: cast and crew, collections, trailers, and a
// series' seasons and episodes, exactly as for a scanned file.
type ItemRefresher interface {
	RefreshItem(ctx context.Context, contentID string) error
}

const (
	enrichWorkers     = 2
	enrichBatch       = 20
	enrichIdle        = time.Minute
	enrichMaxFailures = 3
	// After a failure, wait this long (times the failure count) before trying again.
	enrichRetryAfter = 6 * time.Hour
)

// SetRefresher enables enrichment. Without one, addon titles keep the
// metadata their addon sent.
func (s *Service) SetRefresher(r ItemRefresher) { s.refresher = r }

// nudgeEnrich wakes the enrichment loop after new titles were made.
func (s *Service) nudgeEnrich() {
	select {
	case s.enrichWake <- struct{}{}:
	default:
	}
}

// runEnrichment fills in Quillo-made items in the background, most recently
// shown first, until ctx ends.
func (s *Service) runEnrichment(ctx context.Context) {
	if s.refresher == nil {
		return
	}
	for {
		n := s.enrichBatch(ctx)
		if ctx.Err() != nil {
			return
		}
		if n == enrichBatch {
			continue // more waiting
		}
		select {
		case <-ctx.Done():
			return
		case <-s.enrichWake:
		case <-time.After(enrichIdle):
		}
	}
}

func (s *Service) enrichBatch(ctx context.Context) int {
	rows, err := s.store.db.Query(ctx, `
		SELECT content_id FROM quillo_items
		WHERE enriched_at IS NULL AND enrich_failures < $1
		  AND (enrich_tried_at IS NULL OR enrich_tried_at < now() - make_interval(secs => $2 * enrich_failures))
		ORDER BY last_seen_at DESC
		LIMIT $3`, enrichMaxFailures, enrichRetryAfter.Seconds(), enrichBatch)
	if err != nil {
		slog.WarnContext(ctx, "quillo enrichment: listing items", "error", err)
		return 0
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	work := make(chan string)
	done := make(chan struct{})
	for range enrichWorkers {
		go func() {
			defer func() { done <- struct{}{} }()
			for id := range work {
				s.enrichOne(ctx, id)
			}
		}()
	}
	for _, id := range ids {
		select {
		case work <- id:
		case <-ctx.Done():
		}
	}
	close(work)
	for range enrichWorkers {
		<-done
	}
	return len(ids)
}

func (s *Service) enrichOne(ctx context.Context, contentID string) {
	rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	err := s.refresher.RefreshItem(rctx, contentID)
	if err != nil {
		slog.InfoContext(ctx, "quillo enrichment: provider refresh failed; keeping addon metadata",
			"content_id", contentID, "error", err)
		_, _ = s.store.db.Exec(ctx, `UPDATE quillo_items
			SET enrich_failures = enrich_failures + 1, enrich_tried_at = now() WHERE content_id = $1`, contentID)
		return
	}
	_, _ = s.store.db.Exec(ctx, `UPDATE quillo_items
		SET enriched_at = now(), enrich_tried_at = now() WHERE content_id = $1`, contentID)
	s.afterEnrich(ctx, contentID)
}
