package handlers

import (
	"context"
	"sync"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

// Quillo: addon results in catalog text search (FORK_NOTES.md).

// SearchMergeStart begins extra searches for a catalog text search before the
// catalog's own search runs. The returned function waits for them (within
// their own deadline) and merges their items into the catalog's first page.
type SearchMergeStart func(ctx context.Context, req catalog.CatalogRequest, access catalog.AccessFilter) func(local []*models.MediaItem) []*models.MediaItem

var (
	searchMergeMu sync.RWMutex
	searchMerge   SearchMergeStart
)

// RegisterSearchMerge installs the extra search used by Browse.
func RegisterSearchMerge(start SearchMergeStart) {
	searchMergeMu.Lock()
	defer searchMergeMu.Unlock()
	searchMerge = start
}

// startSearchMerge returns nil unless req is the first page of a text search
// and a merge is registered.
func startSearchMerge(ctx context.Context, req catalog.CatalogRequest, access catalog.AccessFilter) func([]*models.MediaItem) []*models.MediaItem {
	if req.SearchQuery == "" || req.Offset != 0 || req.After != nil {
		return nil
	}
	searchMergeMu.RLock()
	start := searchMerge
	searchMergeMu.RUnlock()
	if start == nil {
		return nil
	}
	return start(ctx, req, access)
}
