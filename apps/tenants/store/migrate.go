package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLockID serialises concurrent migration runs (pg_advisory_xact_lock).
const migrationLockID = 5_100_001

// PlatformTables have a tenant key but hold no tenant's data, so they have no
// RLS. Every other table with a tenant_id column must have RLS enabled and
// forced (ADR-0002); adding a table here needs the owner's review.
var PlatformTables = []string{"public.tenants"}

// Migrate applies the embedded migrations in file name order. Each file runs in
// its own transaction and is recorded in schema_migrations. It must run as the
// owner role of the database, not as the application role.
func Migrate(ctx context.Context, conn *pgx.Conn) error {
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    text        PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, name := range names {
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		version := name[len("migrations/"):]
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
				return err
			}
			var applied bool
			if err := tx.QueryRow(ctx,
				"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version,
			).Scan(&applied); err != nil {
				return err
			}
			if applied {
				return nil
			}
			if _, err := tx.Exec(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version)
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", version, err)
		}
	}
	return nil
}

// UnprotectedTables lists tables with a tenant_id column that do not have RLS
// both enabled and forced, except the PlatformTables.
func UnprotectedTables(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, err := conn.Query(ctx, `
		SELECT n.nspname || '.' || c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
		WHERE c.relkind IN ('r', 'p')
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		  AND NOT (c.relrowsecurity AND c.relforcerowsecurity)
		  AND NOT (n.nspname || '.' || c.relname = ANY ($1))
		ORDER BY 1`, PlatformTables)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Connect retries until ctx is done, because on a fresh cluster the database
// may still be starting when the migration job runs.
func Connect(ctx context.Context, log *slog.Logger, dsn string) (*pgx.Conn, error) {
	for {
		conn, err := pgx.Connect(ctx, dsn)
		if err == nil {
			return conn, nil
		}
		log.Warn("database not reachable, retrying", "err", err)
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(5 * time.Second):
		}
	}
}
