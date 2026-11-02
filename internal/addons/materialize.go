package addons

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/contentid"
	"github.com/Silo-Server/silo-server/internal/models"
)

// Materializer turns addon metas into catalog items the Silo apps can show:
// rows in media_items keyed by Silo's own deterministic content ids. A title
// already in a library resolves to that item, so local files and addon
// results for the same title are one item. New items belong to no library,
// so library pages, Recently Added and Silo search never list them.
type Materializer struct {
	db    *pgxpool.Pool
	items *catalog.ItemRepository
}

func NewMaterializer(db *pgxpool.Pool) *Materializer {
	return &Materializer{db: db, items: catalog.NewItemRepository(db)}
}

// itemType maps a Stremio type to a Silo item type; other types (channels,
// live TV) are not shown.
func itemType(stremioType string) (string, bool) {
	switch stremioType {
	case "movie":
		return "movie", true
	case "series":
		return "series", true
	}
	return "", false
}

type resolvedMeta struct {
	meta      *MetaItem
	itemType  string
	ids       contentid.ProviderIDs
	contentID string
}

// Items makes sure every usable meta has a catalog item and returns the items
// in the metas' order, skipping metas Quillo can't identify. Existing items
// keep their metadata; only items Quillo made are recorded as seen.
func (m *Materializer) Items(ctx context.Context, metas []MetaItem) ([]*models.MediaItem, error) {
	resolved := make([]*resolvedMeta, 0, len(metas))
	for i := range metas {
		meta := &metas[i]
		typ, ok := itemType(meta.Type)
		if !ok || strings.TrimSpace(meta.Name) == "" {
			continue
		}
		ids := contentid.ProviderIDs{Imdb: meta.IMDb(), Tmdb: string(meta.MovieDBID)}
		if typ == "series" {
			ids.Tvdb = string(meta.TVDBID)
		}
		if ids.Imdb == "" && ids.Tmdb == "" && ids.Tvdb == "" {
			continue
		}
		resolved = append(resolved, &resolvedMeta{meta: meta, itemType: typ, ids: ids})
	}
	if len(resolved) == 0 {
		return nil, nil
	}
	if err := m.resolveExisting(ctx, resolved); err != nil {
		return nil, err
	}
	keep := resolved[:0]
	for _, r := range resolved {
		if r.contentID == "" {
			var ok bool
			if r.itemType == "movie" {
				r.contentID, ok = contentid.ForMovie(r.ids)
			} else {
				r.contentID, ok = contentid.ForSeries(r.ids)
			}
			if !ok {
				continue
			}
		}
		keep = append(keep, r)
	}
	if err := m.upsert(ctx, keep); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(keep))
	for _, r := range keep {
		ids = append(ids, r.contentID)
	}
	items, err := m.items.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*models.MediaItem, len(items))
	for _, it := range items {
		byID[it.ContentID] = it
	}
	out := make([]*models.MediaItem, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if it := byID[id]; it != nil && !seen[id] {
			out = append(out, it)
			seen[id] = true
		}
	}
	return out, nil
}

// resolveExisting finds items that already carry one of each meta's provider
// ids, so a title in a library (or made earlier) is reused, whatever anchor
// its content id was built from.
func (m *Materializer) resolveExisting(ctx context.Context, rs []*resolvedMeta) error {
	var providers, providerIDs, types []string
	for _, r := range rs {
		for provider, id := range map[string]string{"imdb": r.ids.Imdb, "tmdb": r.ids.Tmdb, "tvdb": r.ids.Tvdb} {
			if id != "" {
				providers = append(providers, provider)
				providerIDs = append(providerIDs, id)
				types = append(types, r.itemType)
			}
		}
	}
	rows, err := m.db.Query(ctx, `
		SELECT p.provider, p.provider_id, p.item_type, p.content_id
		FROM unnest($1::text[], $2::text[], $3::text[]) AS q(provider, provider_id, item_type)
		JOIN media_item_provider_ids p
		  ON p.provider = q.provider AND p.provider_id = q.provider_id AND p.item_type = q.item_type`,
		providers, providerIDs, types)
	if err != nil {
		return fmt.Errorf("looking up addon titles: %w", err)
	}
	found, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) ([4]string, error) {
		var v [4]string
		err := row.Scan(&v[0], &v[1], &v[2], &v[3])
		return v, err
	})
	if err != nil {
		return fmt.Errorf("looking up addon titles: %w", err)
	}
	type key struct{ provider, id, typ string }
	index := make(map[key]string, len(found))
	for _, f := range found {
		index[key{f[0], f[1], f[2]}] = f[3]
	}
	for _, r := range rs {
		// TMDB and TVDB anchor content ids, so prefer them over IMDb.
		for _, k := range []key{{"tmdb", r.ids.Tmdb, r.itemType}, {"tvdb", r.ids.Tvdb, r.itemType}, {"imdb", r.ids.Imdb, r.itemType}} {
			if k.id == "" {
				continue
			}
			if id, ok := index[k]; ok {
				r.contentID = id
				break
			}
		}
	}
	return nil
}

// metahub serves several poster sizes; "small" looks soft on a TV.
func posterURL(u string) string {
	return strings.Replace(u, "/poster/small/", "/poster/medium/", 1)
}

func (m *Materializer) upsert(ctx context.Context, rs []*resolvedMeta) error {
	// Write in content-id order so concurrent catalog loads that share titles
	// take row locks in the same order, and retry once if they still collide.
	sorted := slices.Clone(rs)
	slices.SortFunc(sorted, func(a, b *resolvedMeta) int { return strings.Compare(a.contentID, b.contentID) })
	err := m.upsertOnce(ctx, sorted)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "40P01" {
		err = m.upsertOnce(ctx, sorted)
	}
	if err != nil {
		return fmt.Errorf("saving addon titles: %w", err)
	}
	return nil
}

func (m *Materializer) upsertOnce(ctx context.Context, rs []*resolvedMeta) error {
	batch := &pgx.Batch{}
	seen := make(map[string]bool, len(rs))
	for _, r := range rs {
		if seen[r.contentID] {
			continue
		}
		seen[r.contentID] = true
		meta := r.meta
		genres := meta.Genres
		if genres == nil {
			genres = []string{}
		}
		// A new item only: an existing one, made by Silo or an earlier visit,
		// keeps its metadata.
		batch.Queue(`
			WITH ins AS (
				INSERT INTO media_items (content_id, type, title, year, overview, genres, runtime,
					imdb_id, tmdb_id, tvdb_id, rating_imdb, poster_path, backdrop_path, logo_path, show_status, status)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 'matched')
				ON CONFLICT (content_id) DO NOTHING
				RETURNING content_id
			)
			INSERT INTO quillo_items (content_id, source_id, source_type)
			SELECT content_id, $16, $17 FROM ins`,
			r.contentID, r.itemType, meta.Name, meta.YearInt(), meta.Description, genres, meta.RuntimeMinutes(),
			r.ids.Imdb, r.ids.Tmdb, r.ids.Tvdb, meta.Rating(), posterURL(meta.Poster), meta.Background, meta.Logo,
			meta.Status, meta.ID, meta.Type)
		for provider, id := range map[string]string{"imdb": r.ids.Imdb, "tmdb": r.ids.Tmdb, "tvdb": r.ids.Tvdb} {
			if id == "" {
				continue
			}
			// Skipped when another item already owns this provider id.
			batch.Queue(`INSERT INTO media_item_provider_ids (content_id, provider, provider_id, item_type)
				VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, r.contentID, provider, id, r.itemType)
		}
		batch.Queue(`UPDATE quillo_items SET last_seen_at = now()
			WHERE content_id = $1 AND last_seen_at < now() - interval '10 minutes'`, r.contentID)
	}
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
