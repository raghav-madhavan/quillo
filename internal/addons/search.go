package addons

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

// Search waits this long for addons at most. Results that arrive later are
// dropped for this search; local results never wait longer than this.
const (
	searchDeadline    = 3500 * time.Millisecond
	searchPerCatalog  = 20
	searchResultTTL   = 10 * time.Minute
	searchMaxQueryLen = 200
)

// searchTypes is the item types a catalog search scope asks addons for.
func searchTypes(req catalog.CatalogRequest) []string {
	scope := req.SearchMediaScope
	if scope == "" {
		scope = req.Query.MediaScope
	}
	switch strings.ToLower(scope) {
	case "", catalog.MediaScopeVideo, catalog.MediaScopeVideoWithEpisodes:
		return []string{"movie", "series"}
	case "movie":
		return []string{"movie"}
	case "series":
		return []string{"series"}
	}
	return nil
}

type searchHit struct {
	ids []string
	at  time.Time
}

// searchCache keeps recent addon search results, so typing the same query
// again (or paging back) doesn't ask every addon again.
type searchCache struct {
	mu      sync.Mutex
	entries map[string]searchHit
}

func (c *searchCache) get(key string) ([]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.entries[key]
	if !ok || time.Since(h.at) > searchResultTTL {
		return nil, false
	}
	return h.ids, true
}

func (c *searchCache) put(key string, ids []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) > 500 {
		clear(c.entries)
	}
	c.entries[key] = searchHit{ids: ids, at: time.Now()}
}

// StartSearch is a handlers.SearchMergeStart: it asks every enabled addon
// with a searchable catalog in parallel, and the returned function appends
// their titles after the local results, without duplicates.
func (s *Service) StartSearch(ctx context.Context, req catalog.CatalogRequest, access catalog.AccessFilter) func([]*models.MediaItem) []*models.MediaItem {
	q := strings.TrimSpace(req.SearchQuery)
	types := searchTypes(req)
	if q == "" || len(q) > searchMaxQueryLen || len(types) == 0 {
		return nil
	}
	key := strings.ToLower(q) + "|" + strings.Join(types, ",")
	deadline := time.NewTimer(searchDeadline)
	done := make(chan []string, 1)
	if ids, ok := s.search.get(key); ok {
		done <- ids
	} else {
		go func() {
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), searchDeadline)
			defer cancel()
			ids := s.searchAddons(sctx, q, types)
			s.search.put(key, ids)
			done <- ids
		}()
	}
	return func(local []*models.MediaItem) []*models.MediaItem {
		defer deadline.Stop()
		var ids []string
		select {
		case ids = <-done:
		case <-deadline.C:
			return local
		case <-ctx.Done():
			return local
		}
		if len(ids) == 0 {
			return local
		}
		seen := make(map[string]bool, len(local))
		for _, it := range local {
			seen[it.ContentID] = true
		}
		fresh := ids[:0:0]
		for _, id := range ids {
			if !seen[id] {
				fresh = append(fresh, id)
				seen[id] = true
			}
		}
		items, err := s.items.GetByIDsWithAccess(ctx, fresh, access)
		if err != nil {
			slog.WarnContext(ctx, "quillo addon search: loading items", "error", err)
			return local
		}
		byID := make(map[string]*models.MediaItem, len(items))
		for _, it := range items {
			byID[it.ContentID] = it
		}
		found := make([]*models.MediaItem, 0, len(fresh))
		for _, id := range fresh {
			if it := byID[id]; it != nil {
				found = append(found, it)
			}
		}
		// Addons answer per type, so a movie list would bury an exact series
		// match. Closer title matches go first; addon order breaks ties.
		lq := strings.ToLower(q)
		slices.SortStableFunc(found, func(a, b *models.MediaItem) int {
			return titleRank(a.Title, lq) - titleRank(b.Title, lq)
		})
		return append(local, found...)
	}
}

// titleRank orders a title by how well it matches the lowercased query.
func titleRank(title, lq string) int {
	t := strings.ToLower(title)
	switch {
	case t == lq:
		return 0
	case strings.HasPrefix(t, lq):
		return 1
	case strings.Contains(t, lq):
		return 2
	}
	return 3
}

// searchAddons asks every enabled server-wide addon's searchable catalogs and
// returns the content ids in addon order, each addon's results in its own
// order. An addon that fails or runs out of time is skipped.
func (s *Service) searchAddons(ctx context.Context, q string, types []string) []string {
	addons, err := s.store.List(ctx, nil)
	if err != nil {
		slog.WarnContext(ctx, "quillo addon search: listing addons", "error", err)
		return nil
	}
	type job struct {
		addon *Addon
		cat   Catalog
	}
	var jobs []job
	for _, a := range addons {
		if !a.Enabled {
			continue
		}
		for _, c := range a.Manifest.Catalogs {
			if c.SupportsExtra("search") && slices.Contains(types, c.Type) {
				jobs = append(jobs, job{a, c})
			}
		}
	}
	results := make([][]MetaItem, len(jobs))
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			metas, err := s.client.FetchCatalog(ctx, BaseURL(j.addon.ManifestURL), j.cat.Type, j.cat.ID, map[string]string{"search": q})
			if err != nil {
				slog.DebugContext(ctx, "quillo addon search: catalog failed", "addon", j.addon.Name, "catalog", j.cat.Type+"/"+j.cat.ID, "error", err)
				return
			}
			if len(metas) > searchPerCatalog {
				metas = metas[:searchPerCatalog]
			}
			results[i] = metas
		}()
	}
	wg.Wait()

	var metas []MetaItem
	for _, r := range results {
		metas = append(metas, r...)
	}
	if len(metas) == 0 {
		return nil
	}
	items, err := s.mat.Items(ctx, metas)
	if err != nil {
		slog.WarnContext(ctx, "quillo addon search: saving titles", "error", err)
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ContentID)
	}
	return ids
}

