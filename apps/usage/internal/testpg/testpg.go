// Package testpg gives tests a fresh, migrated usage database. It needs a
// superuser DSN of a PostgreSQL server with TimescaleDB (TSL) in
// TEST_DATABASE_URL, e.g. postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
// against timescale/timescaledb:2.27.2-pg18. CI sets it
// (.github/workflows/usage.yml); locally the tests are skipped without it.
package testpg

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/migrations"
)

const (
	OwnerRole  = "usage_owner"
	WriterRole = "usage_writer"
	ReaderRole = "usage_reader" // NOLOGIN here; tests use SET ROLE
	password   = "test-only-password"
)

type DB struct {
	Name      string
	Super     *pgx.Conn // superuser on this database; bypasses RLS
	Owner     *pgx.Conn
	WriterDSN string
}

func dsnFor(t testing.TB, base, user, db string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL must be a URL: %v", err)
	}
	if user != "" {
		u.User = url.UserPassword(user, password)
	}
	u.Path = "/" + db
	return u.String()
}

// New creates the roles and a fresh database with the timescaledb extension,
// like the usage cluster's managed roles and Database resource do, and applies
// the migrations as the owner.
func New(t testing.TB) DB {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close(ctx) })

	if err := setupRoles(ctx, admin); err != nil {
		t.Fatal(err)
	}

	name := fmt.Sprintf("usage_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s", name, OwnerRole)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", name)) })

	super, err := pgx.Connect(ctx, dsnFor(t, base, "", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { super.Close(ctx) })
	if _, err := super.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS timescaledb"); err != nil {
		t.Fatal(err)
	}

	owner, err := pgx.Connect(ctx, dsnFor(t, base, OwnerRole, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close(ctx) })
	if err := migrations.Migrate(ctx, owner); err != nil {
		t.Fatal(err)
	}
	pauseJobs(t, super)
	return DB{Name: name, Super: super, Owner: owner, WriterDSN: dsnFor(t, base, WriterRole, name)}
}

// rolesLock is the advisory lock key that serializes setupRoles across test
// processes ("usage" in hex). All callers connect to the database of
// TEST_DATABASE_URL, so they share one advisory lock space.
const rolesLock = 0x7573616765

// setupRoles creates the shared roles and sets their passwords. The roles are
// cluster-wide, and go test runs the store and writer packages in parallel
// against one server: two ALTER ROLE on the same role at the same time fail
// with "tuple concurrently updated", and two CREATE ROLE with a duplicate key
// (HAMSP-32). The transaction-level advisory lock runs one setup at a time.
func setupRoles(ctx context.Context, admin *pgx.Conn) error {
	return pgx.BeginFunc(ctx, admin, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", rolesLock); err != nil {
			return fmt.Errorf("roles lock: %w", err)
		}
		for _, stmt := range []string{
			fmt.Sprintf(`DO $$ BEGIN
				IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%[1]s') THEN CREATE ROLE %[1]s LOGIN; END IF;
				IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%[2]s') THEN CREATE ROLE %[2]s LOGIN; END IF;
				IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%[3]s') THEN CREATE ROLE %[3]s NOLOGIN; END IF;
			END $$`, OwnerRole, WriterRole, ReaderRole),
			fmt.Sprintf("ALTER ROLE %s PASSWORD '%s'", OwnerRole, password),
			fmt.Sprintf("ALTER ROLE %s PASSWORD '%s' NOSUPERUSER NOBYPASSRLS", WriterRole, password),
		} {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	})
}

// Count returns the stored rows with the given event IDs, as the superuser.
func (db DB) Count(t testing.TB, ids []string) int {
	t.Helper()
	var n int
	if err := db.Super.QueryRow(context.Background(),
		"SELECT count(*) FROM usage_events WHERE event_id::text = ANY($1)", ids).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// pauseJobs unschedules the policies the migrations created, so only the tests
// run them (CALL run_job runs an unscheduled job too). Otherwise the background
// scheduler may start a policy while a test runs the same one, and a second
// refresh of a continuous aggregate fails with 55P03 (HAMSP-32). A run the
// scheduler started before the jobs were paused is waited for.
func pauseJobs(t testing.TB, super *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	// Jobs below 1000 are TimescaleDB's own (telemetry, job history).
	if _, err := super.Exec(ctx,
		"SELECT alter_job(job_id, scheduled => false) FROM timescaledb_information.jobs WHERE job_id >= 1000"); err != nil {
		t.Fatalf("pause jobs: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		var running int
		if err := super.QueryRow(ctx,
			"SELECT count(*) FROM timescaledb_information.job_stats WHERE job_id >= 1000 AND job_status = 'Running'").Scan(&running); err != nil {
			t.Fatalf("job stats: %v", err)
		}
		if running == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d jobs still running after 30s", running)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
