package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/exampleapp/core"
)

// Needs a superuser DSN in TEST_DATABASE_URL; skipped without it.

const (
	ownerRole = "exampleapp_owner"
	appRole   = "exampleapp"
	testPass  = "test-only-password"
)

type testDB struct {
	owner  *pgx.Conn
	appDSN string
}

func dsnFor(t *testing.T, base, user, db string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL must be a URL: %v", err)
	}
	u.User = url.UserPassword(user, testPass)
	u.Path = "/" + db
	return u.String()
}

func newTestDB(t *testing.T) testDB {
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
		END $$`, ownerRole, appRole),
		fmt.Sprintf("ALTER ROLE %s PASSWORD '%s'", ownerRole, testPass),
		fmt.Sprintf("ALTER ROLE %s PASSWORD '%s' NOSUPERUSER NOBYPASSRLS", appRole, testPass),
	} {
		if _, err := super.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	db := fmt.Sprintf("exampleapp_test_%d", time.Now().UnixNano())
	if _, err := super.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s", db, ownerRole)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = super.Exec(ctx, fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", db)) })

	owner, err := pgx.Connect(ctx, dsnFor(t, base, ownerRole, db))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close(ctx) })
	if err := Migrate(ctx, owner); err != nil {
		t.Fatal(err)
	}
	return testDB{owner: owner, appDSN: dsnFor(t, base, appRole, db)}
}

func openStore(t *testing.T, db testDB) *Postgres {
	t.Helper()
	st, err := Open(context.Background(), db.appDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestMigrationsLeaveNoUnprotectedTables(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := Migrate(ctx, db.owner); err != nil {
		t.Fatalf("second run must be a no-op: %v", err)
	}
	got, err := UnprotectedTables(ctx, db.owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 0 {
		t.Fatalf("tables with tenant_id but without enabled and forced RLS: %v", got)
	}
}

func TestUnprotectedTablesFlagsTableWithoutRLS(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	fixture, err := os.ReadFile("testdata/unprotected_tables.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.owner.Exec(ctx, string(fixture)); err != nil {
		t.Fatal(err)
	}
	got, err := UnprotectedTables(ctx, db.owner)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"public.no_rls_notes", "public.not_forced_notes"}
	if !slices.Equal(got, want) {
		t.Fatalf("UnprotectedTables = %v, want %v", got, want)
	}
}

// Role attributes of the real cluster are checked by the e2e tests.
func TestItemsTableHasForcedRLSAndForeignOwner(t *testing.T) {
	db := newTestDB(t)
	var enabled, forced bool
	var owner string
	err := db.owner.QueryRow(context.Background(), `
		SELECT c.relrowsecurity, c.relforcerowsecurity, pg_get_userbyid(c.relowner)
		FROM pg_class c WHERE c.oid = 'public.items'::regclass`).Scan(&enabled, &forced, &owner)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled || !forced {
		t.Fatalf("items: relrowsecurity=%v relforcerowsecurity=%v, want both true", enabled, forced)
	}
	if owner == appRole {
		t.Fatalf("items is owned by the application role %q", appRole)
	}
}

func TestNoTenantContextFailsClosed(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := openStore(t, db).Create(ctx, "tenant-a", "secret"); err != nil {
		t.Fatal(err)
	}

	app, err := pgx.Connect(ctx, db.appDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(ctx)

	var n int
	if err := app.QueryRow(ctx, "SELECT count(*) FROM items").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("SELECT without tenant context returned %d rows, want 0", n)
	}
	if _, err := app.Exec(ctx, "INSERT INTO items (tenant_id, name) VALUES ('tenant-a', 'x')"); err == nil {
		t.Fatal("INSERT without tenant context succeeded, want an RLS error")
	}
}

func TestStoreIsolatesTenants(t *testing.T) {
	st := openStore(t, newTestDB(t))
	ctx := context.Background()
	a, err := st.Create(ctx, "tenant-a", "a-item")
	if err != nil {
		t.Fatal(err)
	}
	if a.TenantID != "tenant-a" {
		t.Fatalf("TenantID = %q", a.TenantID)
	}

	if _, err := st.Get(ctx, "tenant-b", a.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Get by B: err = %v, want ErrNotFound", err)
	}
	if _, err := st.Update(ctx, "tenant-b", a.ID, "hijacked"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Update by B: err = %v, want ErrNotFound", err)
	}
	if err := st.Delete(ctx, "tenant-b", a.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Delete by B: err = %v, want ErrNotFound", err)
	}
	if items, err := st.List(ctx, "tenant-b"); err != nil || len(items) != 0 {
		t.Errorf("List by B = %v, %v; want empty", items, err)
	}

	got, err := st.Get(ctx, "tenant-a", a.ID)
	if err != nil || got.Name != "a-item" {
		t.Fatalf("Get by A = %+v, %v; want unchanged item", got, err)
	}
}

// Verifies: exampleapp AC-1; T-12
func TestStatementTimeoutIsQueryTimeout(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	u, err := url.Parse(db.appDSN)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("statement_timeout", "300ms")
	u.RawQuery = q.Encode()
	st, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)

	lock, err := db.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Rollback(ctx) })
	if _, err := lock.Exec(ctx, "LOCK TABLE items IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.List(ctx, "tenant-a"); !errors.Is(err, core.ErrQueryTimeout) {
		t.Fatalf("List on a locked table: %v, want core.ErrQueryTimeout", err)
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.List(ctx, "tenant-a"); err != nil {
		t.Fatalf("List after the lock is gone: %v", err)
	}
}

// Verifies: exampleapp AC-1
func TestPoolerTimeoutIsQueryTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"PgBouncer query_timeout", &pgconn.PgError{Severity: "FATAL", Code: "08P01", Message: "query timeout"}, true},
		{"PostgreSQL statement_timeout", fmt.Errorf("list: %w", &pgconn.PgError{Code: "57014"}), true},
		{"another PgBouncer protocol error", &pgconn.PgError{Code: "08P01", Message: "server conn crashed?"}, false},
		{"connection refused", errors.New("dial tcp: connection refused"), false},
	} {
		if got := isQueryTimeout(tc.err); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}
