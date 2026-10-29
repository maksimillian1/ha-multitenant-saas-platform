package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLockID serialises concurrent migration runs (pg_advisory_xact_lock).
const migrationLockID = 5_000_011

// RequireTimescaleDB fails unless the TimescaleDB extension is installed in the
// database with its Timescale License features (ADR-0008). CNPG's Database
// resource creates the extension; the migrations need its columnstore and
// continuous aggregates, which the Apache 2 edition does not have.
func RequireTimescaleDB(ctx context.Context, conn *pgx.Conn) error {
	var version, license string
	err := conn.QueryRow(ctx, `SELECT extversion, current_setting('timescaledb.license')
		FROM pg_extension WHERE extname = 'timescaledb'`).Scan(&version, &license)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("the timescaledb extension is not installed in this database")
	}
	if err != nil {
		return err
	}
	if license != "timescale" {
		return fmt.Errorf("timescaledb %s runs with license %q, want \"timescale\" (TSL features)", version, license)
	}
	return nil
}

// Migrate applies the embedded migrations in file name order. Each file runs in
// its own transaction and is recorded in schema_migrations. It must run as the
// owner role of the database, not as Usage Writer's role.
func Migrate(ctx context.Context, conn *pgx.Conn) error {
	if err := RequireTimescaleDB(ctx, conn); err != nil {
		return err
	}
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

// UnprotectedTables returns every relation with a tenant_id column, chunks and
// partitions included, that an application role could use to reach another
// tenant's rows. An empty result means tenant isolation holds.
//
// The usage database cannot use RLS everywhere (ADR-0008), so a table passes
// if it has RLS enabled and forced, or if nobody but its owner has a privilege
// on it. A view passes if nobody but its owner has a privilege on it, or if it
// is a security_barrier view that filters by app.current_tenant_id.
func UnprotectedTables(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, err := conn.Query(ctx, `
		SELECT n.nspname || '.' || c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
		WHERE c.relkind IN ('r', 'p', 'v', 'm')
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		  AND EXISTS (SELECT 1 FROM aclexplode(c.relacl) acl WHERE acl.grantee <> c.relowner)
		  AND NOT CASE WHEN c.relkind IN ('r', 'p') THEN c.relrowsecurity AND c.relforcerowsecurity
		               WHEN c.relkind = 'v' THEN coalesce('security_barrier=true' = ANY (c.reloptions), false)
		                                         AND pg_get_viewdef(c.oid) LIKE '%app.current_tenant_id%'
		               ELSE false END
		ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
