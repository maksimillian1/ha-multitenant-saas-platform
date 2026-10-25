package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLockID serialises concurrent migration runs (pg_advisory_xact_lock).
const migrationLockID = 5_000_011

// CreateDatabase creates the database owned by owner if it does not exist. It
// needs a superuser connection: CNPG 1.23 has no Database resource, so the
// migrate job creates the usage database itself (ADR-0006).
func CreateDatabase(ctx context.Context, admin *pgx.Conn, name, owner string) (created bool, err error) {
	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	_, err = admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s",
		pgx.Identifier{name}.Sanitize(), pgx.Identifier{owner}.Sanitize()))
	return err == nil, err
}

// Migrate applies the embedded migrations in file name order. Each file runs in
// its own transaction and is recorded in schema_migrations. It must run as the
// owner role of the database, not as Usage Writer's role.
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

// UnprotectedTables returns every table with a tenant_id column, partitions
// included, that does not have row level security both enabled and forced. An
// empty result means every multi-tenant table is covered by RLS (QS-01).
func UnprotectedTables(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, err := conn.Query(ctx, `
		SELECT n.nspname || '.' || c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
		WHERE c.relkind IN ('r', 'p')
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		  AND NOT (c.relrowsecurity AND c.relforcerowsecurity)
		ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
