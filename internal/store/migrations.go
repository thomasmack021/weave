package store

import (
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the "pgx5" database driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies all pending up-migrations to the database identified by dsn
// (a standard postgres:// / postgresql:// / pgx5:// URL). It is idempotent: a
// fully-migrated database is a no-op.
func Migrate(dsn string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("store: loading embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, migrateDSN(dsn))
	if err != nil {
		return fmt.Errorf("store: initializing migrator: %w", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("store: applying migrations: %w", err)
	}
	return nil
}

// migrateDSN rewrites a postgres:// URL to the pgx5:// scheme golang-migrate's
// pgx/v5 driver registers under, leaving an already-pgx5 URL untouched.
func migrateDSN(dsn string) string {
	for _, p := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(dsn, p) {
			return "pgx5://" + strings.TrimPrefix(dsn, p)
		}
	}
	return dsn
}
