package catalog

import "sync"

// Quillo: virtual libraries (FORK_NOTES.md). A virtual library has no folders
// on disk; another package fills its memberships (Quillo's "Streaming" library
// of addon titles). Scans skip it, and membership reconciliation, which drops
// memberships with no present file, leaves it alone, so its titles are never
// unlinked or purged as orphans.

var (
	virtualMu        sync.RWMutex
	virtualLibraries = map[int]bool{}
)

// RegisterVirtualLibrary marks folderID as a virtual library.
func RegisterVirtualLibrary(folderID int) {
	virtualMu.Lock()
	defer virtualMu.Unlock()
	virtualLibraries[folderID] = true
}

// IsVirtualLibrary reports whether folderID was registered as virtual.
func IsVirtualLibrary(folderID int) bool {
	virtualMu.RLock()
	defer virtualMu.RUnlock()
	return virtualLibraries[folderID]
}
