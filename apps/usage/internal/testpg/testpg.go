// Package testpg gives tests a fresh, migrated usage database. It needs a
// PostgreSQL superuser DSN in TEST_DATABASE_URL, e.g.
// postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable.
// CI sets it (.github/workflows/usage.yml); locally the tests are skipped without it.
package testpg

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/store"
)

const (
	OwnerRole  = "usage_owner"
	WriterRole = "usage_writer"
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

// New creates the two roles like the CNPG cluster does, then a fresh database
// through store.CreateDatabase, and applies the migrations as the owner.
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

	for _, stmt := range []string{
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%[1]s') THEN CREATE ROLE %[1]s LOGIN; END IF;
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%[2]s') THEN CREATE ROLE %[2]s LOGIN; END IF;
		END $$`, OwnerRole, WriterRole),
		fmt.Sprintf("ALTER ROLE %s PASSWORD '%s'", OwnerRole, password),
		fmt.Sprintf("ALTER ROLE %s PASSWORD '%s' NOSUPERUSER NOBYPASSRLS", WriterRole, password),
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	name := fmt.Sprintf("usage_test_%d", time.Now().UnixNano())
	if _, err := store.CreateDatabase(ctx, admin, name, OwnerRole); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", name)) })

	owner, err := pgx.Connect(ctx, dsnFor(t, base, OwnerRole, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close(ctx) })
	if err := store.Migrate(ctx, owner); err != nil {
		t.Fatal(err)
	}
	super, err := pgx.Connect(ctx, dsnFor(t, base, "", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { super.Close(ctx) })
	return DB{Name: name, Super: super, Owner: owner, WriterDSN: dsnFor(t, base, WriterRole, name)}
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
