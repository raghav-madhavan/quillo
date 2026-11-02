package addons

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Catalog pages are kept in memory for a short time: long enough that the
// home screen loads from memory, short enough that rows stay current. Only
// the content ids are cached; the items themselves are read from the catalog.
const (
	catalogFresh = 20 * time.Minute
	// A stale page is still shown while a refresh runs in the background, so a
	// slow addon never blanks a row that loaded before.
	catalogStale = 12 * time.Hour
	prewarmEvery = 15 * time.Minute
)

type catalogKey struct {
	addonID int64
	typ, id string
	extra   string
}

type catalogEntry struct {
	contentIDs []string
	fetchedAt  time.Time
}

// CatalogCache fetches addon catalog pages, turns them into catalog items and
// caches the resulting content ids.
type CatalogCache struct {
	client *Client
	mat    *Materializer
	now    func() time.Time
	// onNewItems runs after a page was saved, to wake enrichment.
	onNewItems func()

	mu       sync.Mutex
	entries  map[catalogKey]catalogEntry
	inflight map[catalogKey]chan struct{}
}

func NewCatalogCache(client *Client, mat *Materializer) *CatalogCache {
	return &CatalogCache{
		client: client, mat: mat, now: time.Now,
		entries:  map[catalogKey]catalogEntry{},
		inflight: map[catalogKey]chan struct{}{},
	}
}

// ContentIDs returns a catalog page's content ids: from memory when fresh,
// from memory with a background refresh when stale, and from the addon
// otherwise.
func (c *CatalogCache) ContentIDs(ctx context.Context, a *Addon, typ, id string, extra map[string]string) ([]string, error) {
	key := catalogKey{addonID: a.ID, typ: typ, id: id, extra: extraKey(extra)}
	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	age := c.now().Sub(entry.fetchedAt)
	switch {
	case ok && age < catalogFresh:
		return entry.contentIDs, nil
	case ok && age < catalogStale:
		go func() {
			bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), resourceTimeout+30*time.Second)
			defer cancel()
			if _, err := c.refresh(bg, key, a, extra); err != nil {
				slog.WarnContext(bg, "quillo addon catalog refresh failed; keeping the last page",
					"addon", a.Name, "catalog", typ+"/"+id, "error", err)
			}
		}()
		return entry.contentIDs, nil
	}
	return c.refresh(ctx, key, a, extra)
}

// refresh fetches one page, letting concurrent callers for the same page wait
// for a single request.
func (c *CatalogCache) refresh(ctx context.Context, key catalogKey, a *Addon, extra map[string]string) ([]string, error) {
	c.mu.Lock()
	if wait, busy := c.inflight[key]; busy {
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		c.mu.Lock()
		entry, ok := c.entries[key]
		c.mu.Unlock()
		if ok {
			return entry.contentIDs, nil
		}
		return nil, userErrorf("This addon isn't responding.")
	}
	done := make(chan struct{})
	c.inflight[key] = done
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.inflight, key)
		c.mu.Unlock()
		close(done)
	}()

	metas, err := c.client.FetchCatalog(ctx, BaseURL(a.ManifestURL), key.typ, key.id, extra)
	if err != nil {
		return nil, err
	}
	items, err := c.mat.Items(ctx, metas)
	if err != nil {
		return nil, err
	}
	if c.onNewItems != nil {
		c.onNewItems()
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ContentID)
	}
	c.mu.Lock()
	c.entries[key] = catalogEntry{contentIDs: ids, fetchedAt: c.now()}
	c.mu.Unlock()
	return ids, nil
}

// Forget drops an addon's cached pages, after it is removed or turned off.
func (c *CatalogCache) Forget(addonID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if k.addonID == addonID {
			delete(c.entries, k)
		}
	}
}

func extraKey(extra map[string]string) string {
	if len(extra) == 0 {
		return ""
	}
	return resourceURL("", "", "", "", extra)
}

// Prewarm keeps the home-row catalogs of every enabled server-wide addon in
// memory, so the home screen never waits on an addon. It runs until ctx ends.
func (s *Service) Prewarm(ctx context.Context) {
	tick := time.NewTicker(prewarmEvery)
	defer tick.Stop()
	for {
		s.prewarmOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Service) prewarmOnce(ctx context.Context) {
	addons, err := s.store.List(ctx, nil)
	if err != nil {
		slog.WarnContext(ctx, "quillo addon prewarm: listing addons", "error", err)
		return
	}
	var wg sync.WaitGroup
	for _, a := range addons {
		if !a.Enabled || !a.Manifest.HasResource("catalog") {
			continue
		}
		for _, cat := range a.Manifest.Catalogs {
			if cat.RequiresExtra() {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				key := catalogKey{addonID: a.ID, typ: cat.Type, id: cat.ID}
				if _, err := s.catalogs.refresh(ctx, key, a, nil); err != nil {
					slog.WarnContext(ctx, "quillo addon prewarm: catalog failed",
						"addon", a.Name, "catalog", cat.Type+"/"+cat.ID, "error", err)
				}
			}()
		}
	}
	wg.Wait()
}
