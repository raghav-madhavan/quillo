package addons

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound means no addon has that id in the requested scope.
var ErrNotFound = errors.New("addon not found")

// Owner is the profile an addon belongs to. A nil *Owner is the server-wide
// set every profile gets. Silo keys profiles by (user_id, id).
type Owner struct {
	UserID    int64
	ProfileID string
}

func ownerArgs(o *Owner) (any, any) {
	if o == nil {
		return nil, nil
	}
	return o.UserID, o.ProfileID
}

// Addon is one installed addon.
type Addon struct {
	ID            int64
	ManifestURL   string
	AddonID       string
	Name          string
	Manifest      *Manifest
	RawManifest   json.RawMessage
	Enabled       bool
	Position      int
	Owner         *Owner
	LastCheckedAt time.Time
	LastError     *string
	CreatedAt     time.Time
}

type Store struct{ db *pgxpool.Pool }

func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

const addonColumns = `id, manifest_url, addon_id, name, manifest, enabled, position,
	owner_user_id, owner_profile_id, last_checked_at, last_error, created_at`

// inScope matches the addons of the owner bound to $1 (user) and $2 (profile).
const inScope = `owner_user_id IS NOT DISTINCT FROM $1 AND owner_profile_id IS NOT DISTINCT FROM $2`

func scanAddon(row pgx.Row) (*Addon, error) {
	var a Addon
	var userID *int64
	var profileID *string
	if err := row.Scan(&a.ID, &a.ManifestURL, &a.AddonID, &a.Name, &a.RawManifest, &a.Enabled,
		&a.Position, &userID, &profileID, &a.LastCheckedAt, &a.LastError, &a.CreatedAt); err != nil {
		return nil, err
	}
	if userID != nil && profileID != nil {
		a.Owner = &Owner{UserID: *userID, ProfileID: *profileID}
	}
	var m Manifest
	if err := json.Unmarshal(a.RawManifest, &m); err != nil {
		return nil, fmt.Errorf("addon %d: stored manifest: %w", a.ID, err)
	}
	a.Manifest = &m
	return &a, nil
}

func scanAddons(rows pgx.Rows) ([]*Addon, error) {
	defer rows.Close()
	var out []*Addon
	for rows.Next() {
		a, err := scanAddon(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func oneAddon(row pgx.Row) (*Addon, error) {
	a, err := scanAddon(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

// List returns one scope's addons in position order.
func (s *Store) List(ctx context.Context, owner *Owner) ([]*Addon, error) {
	u, p := ownerArgs(owner)
	rows, err := s.db.Query(ctx, `SELECT `+addonColumns+` FROM quillo_addons
		WHERE `+inScope+` ORDER BY position, id`, u, p)
	if err != nil {
		return nil, fmt.Errorf("listing addons: %w", err)
	}
	return scanAddons(rows)
}

// ListForProfile returns the enabled addons a profile uses: the server-wide
// set first, then its own, each in position order.
func (s *Store) ListForProfile(ctx context.Context, owner Owner) ([]*Addon, error) {
	rows, err := s.db.Query(ctx, `SELECT `+addonColumns+` FROM quillo_addons
		WHERE enabled AND (owner_user_id IS NULL OR (owner_user_id = $1 AND owner_profile_id = $2))
		ORDER BY owner_user_id NULLS FIRST, position, id`, owner.UserID, owner.ProfileID)
	if err != nil {
		return nil, fmt.Errorf("listing profile addons: %w", err)
	}
	return scanAddons(rows)
}

func (s *Store) Get(ctx context.Context, id int64, owner *Owner) (*Addon, error) {
	u, p := ownerArgs(owner)
	return oneAddon(s.db.QueryRow(ctx, `SELECT `+addonColumns+` FROM quillo_addons
		WHERE `+inScope+` AND id = $3`, u, p, id))
}

// Create installs an addon at the end of its scope's order.
func (s *Store) Create(ctx context.Context, owner *Owner, manifestURL string, m *Manifest, raw []byte) (*Addon, error) {
	u, p := ownerArgs(owner)
	a, err := scanAddon(s.db.QueryRow(ctx, `INSERT INTO quillo_addons
		(owner_user_id, owner_profile_id, manifest_url, addon_id, name, manifest, position)
		VALUES ($1, $2, $3, $4, $5, $6,
			(SELECT COALESCE(MAX(position), -1) + 1 FROM quillo_addons WHERE `+inScope+`))
		RETURNING `+addonColumns, u, p, manifestURL, m.ID, m.Name, raw))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, userErrorf("%s is already installed with that link.", m.Name)
	}
	return a, err
}

func (s *Store) SetEnabled(ctx context.Context, id int64, owner *Owner, enabled bool) (*Addon, error) {
	u, p := ownerArgs(owner)
	return oneAddon(s.db.QueryRow(ctx, `UPDATE quillo_addons SET enabled = $4, updated_at = now()
		WHERE `+inScope+` AND id = $3 RETURNING `+addonColumns, u, p, id, enabled))
}

// SaveManifest records the result of a manifest refresh. A nil manifest keeps
// the stored one and records fetchErr.
func (s *Store) SaveManifest(ctx context.Context, id int64, owner *Owner, m *Manifest, raw []byte, fetchErr error) (*Addon, error) {
	u, p := ownerArgs(owner)
	if m == nil {
		msg := "The last check failed."
		if fetchErr != nil {
			msg = fetchErr.Error()
		}
		return oneAddon(s.db.QueryRow(ctx, `UPDATE quillo_addons
			SET last_checked_at = now(), last_error = $4, updated_at = now()
			WHERE `+inScope+` AND id = $3 RETURNING `+addonColumns, u, p, id, msg))
	}
	return oneAddon(s.db.QueryRow(ctx, `UPDATE quillo_addons
		SET manifest = $4, addon_id = $5, name = $6, last_checked_at = now(), last_error = NULL, updated_at = now()
		WHERE `+inScope+` AND id = $3 RETURNING `+addonColumns, u, p, id, raw, m.ID, m.Name))
}

// Reorder sets the order of a scope's addons. ids must name every addon in
// the scope exactly once.
func (s *Store) Reorder(ctx context.Context, owner *Owner, ids []int64) error {
	u, p := ownerArgs(owner)
	changed := userErrorf("The addon list changed while you were reordering. Reload the page and try again.")
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM quillo_addons WHERE `+inScope, u, p).Scan(&count); err != nil {
			return err
		}
		seen := make(map[int64]bool, len(ids))
		for _, id := range ids {
			seen[id] = true
		}
		if len(seen) != count || len(ids) != count {
			return changed
		}
		for pos, id := range ids {
			tag, err := tx.Exec(ctx, `UPDATE quillo_addons SET position = $4, updated_at = now()
				WHERE `+inScope+` AND id = $3`, u, p, id, pos)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return changed
			}
		}
		return nil
	})
}

func (s *Store) Delete(ctx context.Context, id int64, owner *Owner) error {
	u, p := ownerArgs(owner)
	tag, err := s.db.Exec(ctx, `DELETE FROM quillo_addons WHERE `+inScope+` AND id = $3`, u, p, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Meta reads a one-time marker; ok is false when it was never set.
func (s *Store) Meta(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.db.QueryRow(ctx, `SELECT value FROM quillo_meta WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO quillo_meta (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = now()`, key, value)
	return err
}
