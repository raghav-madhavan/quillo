// Package addons is Quillo's Stremio-style addon manager: it stores the
// addons an administrator installs, fetches and validates their manifests, and
// serves the admin API the web Addons page uses.
//
// Everything here is Quillo's own code. Silo core calls into it from two
// places, both listed in FORK_NOTES.md: the migration runner and the router.
package addons

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const (
	// Separate from Silo's goose_db_version so the two migration sets never
	// see each other's versions.
	migrationTable = "public.quillo_goose_db_version"
	// Separate from Silo's schemaMigrationsLockID.
	migrationLockID int64 = 8_034_219_742
)

// Migrate applies Quillo's pending migrations. Silo's own migrations must
// already have run, since Quillo tables reference Silo tables.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("quillo migrations: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(migrationLockID))
	if err != nil {
		return fmt.Errorf("quillo migrations: lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub,
		goose.WithTableName(migrationTable),
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		return fmt.Errorf("quillo migrations: %w", err)
	}
	defer provider.Close()

	results, err := provider.Up(ctx)
	for _, r := range results {
		if r.Error == nil {
			slog.InfoContext(ctx, "quillo migration applied", "version", r.Source.Version, "duration", r.Duration)
		}
	}
	if err != nil {
		return fmt.Errorf("quillo migrations: %w", err)
	}
	return nil
}
