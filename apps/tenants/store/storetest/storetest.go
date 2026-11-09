// Package storetest gives tests a fresh tenants database. It is imported by
// tests only.
//
// The store package's own tests keep a copy of New, since they cannot import
// this package (it imports store).
//
// The tests need a PostgreSQL superuser DSN in TEST_DATABASE_URL, e.g.
// postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable.
// CI sets it (.github/workflows/tenants.yml); without it they are skipped,
// and CI fails on a skipped test.
package storetest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/store"
)

const (
	OwnerRole = "tenants_owner"
	AppRole   = "tenants"
	testPass  = "test-only-password" // gitleaks:allow
)

type DB struct {
	Super  *pgx.Conn
	Owner  *pgx.Conn
	Name   string
	AppDSN string
}

func dsnFor(t testing.TB, base, user, db string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL must be a URL: %v", err)
	}
	u.User = url.UserPassword(user, testPass)
	u.Path = "/" + db
	return u.String()
}

// New creates the two roles like the CNPG cluster does and a fresh database
// owned by the owner role, then applies the migrations as the owner.
func New(t testing.TB) DB {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	super, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { super.Close(ctx) })

	for _, stmt := range []string{
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%[1]s') THEN CREATE ROLE %[1]s LOGIN; END IF;
			IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%[2]s') THEN CREATE ROLE %[2]s LOGIN; END IF;
		END $$`, OwnerRole, AppRole),
		fmt.Sprintf("ALTER ROLE %s PASSWORD '%s'", OwnerRole, testPass),
		fmt.Sprintf("ALTER ROLE %s PASSWORD '%s' NOSUPERUSER NOBYPASSRLS", AppRole, testPass),
	} {
		if _, err := super.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	db := fmt.Sprintf("tenants_test_%d", time.Now().UnixNano())
	if _, err := super.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s", db, OwnerRole)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = super.Exec(ctx, fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", db)) })

	owner, err := pgx.Connect(ctx, dsnFor(t, base, OwnerRole, db))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close(ctx) })
	if err := store.Migrate(ctx, owner); err != nil {
		t.Fatal(err)
	}
	return DB{Super: super, Owner: owner, Name: db, AppDSN: dsnFor(t, base, AppRole, db)}
}

// Open connects as the application role.
func (db DB) Open(t testing.TB) *store.Postgres {
	t.Helper()
	st, err := store.Open(context.Background(), db.AppDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}
