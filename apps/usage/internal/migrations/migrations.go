// Package migrations is linked only by cmd/migrate, so writeapi and writer cannot change the schema.
package migrations

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed *.sql
var files embed.FS

const migrationLockID = 5_000_011

// The migrations need columnstore and continuous aggregates, missing in the Apache 2 edition (ADR-0008).
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

// Migrate must run as the database owner role, not as Usage Writer's role.
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

	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	for _, name := range names {
		body, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		version := name
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

// RLS is not usable everywhere here (ADR-0008): a relation passes if RLS is forced or only its owner
// has privileges; a view also passes as a security_barrier view filtering by app.current_tenant_id.
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

// On a fresh cluster the database may still be starting when the migration job runs.
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
