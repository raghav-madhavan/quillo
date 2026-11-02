package addons

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service ties the store and the addon client together.
type Service struct {
	store  *Store
	client *Client
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{store: NewStore(db), client: NewClient()}
}

func (s *Service) Store() *Store { return s.store }

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
// server-wide).
func (s *Service) Install(ctx context.Context, owner *Owner, rawURL string) (*Addon, error) {
	u, err := NormalizeManifestURL(rawURL)
	if err != nil {
		return nil, err
	}
	m, raw, err := s.client.FetchManifest(ctx, u)
	if err != nil {
		return nil, err
	}
	return s.store.Create(ctx, owner, u, m, raw)
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
	return s.store.SaveManifest(ctx, id, owner, m, raw, nil)
}
