package addons

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/procgroup"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/sections"
)

// Service ties the addon store, the addon client, the catalog cache and the
// item materializer together.
type Service struct {
	store    *Store
	client   *Client
	mat      *Materializer
	catalogs *CatalogCache
	items    *catalog.ItemRepository
	search   *searchCache

	refresher        ItemRefresher
	enrichWake       chan struct{}
	streamingLibrary int

	files         *scanner.FileRepository
	streams       *streamSet
	remote        *remoteSources
	loopbackToken string
}

var shared struct {
	once sync.Once
	svc  *Service
}

// Shared returns the process's addon service, building it on first use. The
// native API and the Jellyfin-compatible listener both use it, and its
// registrations with Silo (row type, search, playback, process hooks) must
// happen once.
func Shared(db *pgxpool.Pool) *Service {
	shared.once.Do(func() { shared.svc = NewService(db) })
	return shared.svc
}

// NewService builds the service and registers the addon home-row type and
// addon search with Silo.
func NewService(db *pgxpool.Pool) *Service {
	client := NewClient()
	mat := NewMaterializer(db)
	s := &Service{
		store:    NewStore(db),
		client:   client,
		mat:      mat,
		catalogs: NewCatalogCache(client, mat),
		items:    catalog.NewItemRepository(db),
		search:   &searchCache{entries: map[string]searchHit{}},

		enrichWake: make(chan struct{}, 1),
	}
	s.files = scanner.NewFileRepository(db)
	s.streams = &streamSet{fresh: map[string]time.Time{}, inflight: map[string]chan struct{}{}}
	s.remote = &remoteSources{s: s, cache: map[string]playback.RemoteSource{}}
	s.loopbackToken = newLoopbackToken()
	playback.RegisterRemoteSourceResolver(s.remote.resolve)
	playback.RegisterRemoteFailureReporter(s.streamFailed)
	procgroup.RegisterArgRewriter(s.rewriteArg)
	s.catalogs.onNewItems = s.nudgeEnrich
	// Registered before any scan can run, so scans never touch it.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if id, err := s.ensureStreamingLibrary(ctx); err != nil {
		slog.Error("quillo: Streaming library unavailable; addon series won't list episodes", "error", err)
	} else {
		s.streamingLibrary = id
	}
	sections.RegisterExternalSection(SectionType, s.fetchRow)
	handlers.RegisterSearchMerge(s.StartSearch)
	return s
}

func (s *Service) Store() *Store { return s.store }

// Start runs the one-time and background work: the starter preset, home rows
// for addons that don't have them yet, catalog prewarming and metadata
// enrichment. It returns when ctx ends.
func (s *Service) Start(ctx context.Context) {
	go s.runEnrichment(ctx)
	go s.runSweep(ctx)
	s.EnsureStarter(ctx)
	s.syncRows(ctx)
	s.Prewarm(ctx)
}

func (s *Service) syncRows(ctx context.Context) {
	addons, err := s.store.List(ctx, nil)
	if err != nil {
		slog.WarnContext(ctx, "quillo addon rows: listing addons", "error", err)
		return
	}
	for _, a := range addons {
		if err := s.CreateRows(ctx, a); err != nil {
			slog.WarnContext(ctx, "quillo addon rows: creating", "addon", a.Name, "error", err)
		}
	}
}

// Preview fetches and checks a manifest without installing it.
func (s *Service) Preview(ctx context.Context, rawURL string) (string, *Manifest, error) {
	u, err := NormalizeManifestURL(rawURL)
	if err != nil {
		return "", nil, err
	}
	m, _, err := s.client.FetchManifest(ctx, u)
	return u, m, err
}

// Install fetches, checks and stores an addon in the owner's scope (nil for
// server-wide), and adds its catalogs to the home screen.
func (s *Service) Install(ctx context.Context, owner *Owner, rawURL string) (*Addon, error) {
	u, err := NormalizeManifestURL(rawURL)
	if err != nil {
		return nil, err
	}
	m, raw, err := s.client.FetchManifest(ctx, u)
	if err != nil {
		return nil, err
	}
	a, err := s.store.Create(ctx, owner, u, m, raw)
	if err != nil {
		return nil, err
	}
	s.addonStreamsChanged(ctx, a.ID, false)
	if owner == nil {
		if err := s.CreateRows(ctx, a); err != nil {
			slog.WarnContext(ctx, "quillo addon rows: creating", "addon", a.Name, "error", err)
		}
		go s.warm(context.WithoutCancel(ctx), a)
	}
	return a, nil
}

// warm loads a new addon's home-row catalogs so its rows are ready by the
// time someone opens the home screen.
func (s *Service) warm(ctx context.Context, a *Addon) {
	for _, cat := range a.Manifest.Catalogs {
		if cat.RequiresExtra() {
			continue
		}
		key := catalogKey{addonID: a.ID, typ: cat.Type, id: cat.ID}
		if _, err := s.catalogs.refresh(ctx, key, a, nil); err != nil {
			slog.WarnContext(ctx, "quillo addon: first catalog load failed", "addon", a.Name, "catalog", cat.Type+"/"+cat.ID, "error", err)
		}
	}
}

// SetEnabled turns an addon on or off.
func (s *Service) SetEnabled(ctx context.Context, id int64, owner *Owner, enabled bool) (*Addon, error) {
	a, err := s.store.SetEnabled(ctx, id, owner, enabled)
	if err != nil {
		return nil, err
	}
	if !enabled {
		s.catalogs.Forget(id)
	}
	s.addonStreamsChanged(ctx, id, !enabled)
	return a, nil
}

// addonStreamsChanged runs after an addon was turned on or off or removed:
// its streams stop being offered at once when hide is set, and every title
// asks its addons again on its next view.
func (s *Service) addonStreamsChanged(ctx context.Context, addonID int64, hide bool) {
	if hide {
		if _, err := s.store.db.Exec(ctx, `UPDATE media_files SET missing_since = now()
			WHERE missing_since IS NULL AND file_path IN (SELECT file_path FROM quillo_streams WHERE addon_id = $1)`, addonID); err != nil {
			slog.WarnContext(ctx, "quillo streams: hiding an addon's streams", "error", err)
		}
	}
	if _, err := s.store.db.Exec(ctx, `DELETE FROM quillo_stream_sets`); err != nil {
		slog.WarnContext(ctx, "quillo streams: forgetting stream lookups", "error", err)
	}
	if s.streams != nil {
		s.streams.mu.Lock()
		clear(s.streams.fresh)
		s.streams.mu.Unlock()
	}
}

// Remove uninstalls an addon and removes its home rows.
func (s *Service) Remove(ctx context.Context, id int64, owner *Owner) error {
	s.addonStreamsChanged(ctx, id, true)
	if err := s.store.Delete(ctx, id, owner); err != nil {
		return err
	}
	s.catalogs.Forget(id)
	if owner == nil {
		return s.DeleteRows(ctx, id)
	}
	return nil
}

// Refresh fetches the addon's manifest again. A failure is recorded on the
// addon and the last good manifest is kept, so a flaky addon doesn't vanish.
func (s *Service) Refresh(ctx context.Context, id int64, owner *Owner) (*Addon, error) {
	a, err := s.store.Get(ctx, id, owner)
	if err != nil {
		return nil, err
	}
	m, raw, fetchErr := s.client.FetchManifest(ctx, a.ManifestURL)
	if fetchErr != nil {
		return s.store.SaveManifest(ctx, id, owner, nil, nil, fetchErr)
	}
	s.catalogs.Forget(id)
	return s.store.SaveManifest(ctx, id, owner, m, raw, nil)
}
