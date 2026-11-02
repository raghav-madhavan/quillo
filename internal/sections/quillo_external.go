package sections

import (
	"context"
	"sync"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

// Quillo: home rows whose items come from outside the catalog, such as
// Stremio addon catalogs (FORK_NOTES.md). A package registers a fetch function
// for its section type; FetchOne hands sections of that type to it.

// ExternalSectionFetch returns a section's items. It must return quickly from
// a cache when it can, since it runs while the home screen loads.
type ExternalSectionFetch func(ctx context.Context, resolved ResolvedSection, userID int, profileID string, filter catalog.AccessFilter) (SectionWithItems, error)

var (
	externalMu       sync.RWMutex
	externalSections = map[SectionType]ExternalSectionFetch{}
)

// RegisterExternalSection installs fetch for sections of type t. A later call
// for the same type replaces the earlier one.
func RegisterExternalSection(t SectionType, fetch ExternalSectionFetch) {
	externalMu.Lock()
	defer externalMu.Unlock()
	externalSections[t] = fetch
}

func externalSection(t SectionType) (ExternalSectionFetch, bool) {
	externalMu.RLock()
	defer externalMu.RUnlock()
	fetch, ok := externalSections[t]
	return fetch, ok
}
