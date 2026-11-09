package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
)

// These tests need a PostgreSQL superuser DSN in TEST_DATABASE_URL, e.g.
// postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable.
// CI sets it (.github/workflows/tenants.yml); locally they are skipped without it.

const (
	ownerRole = "tenants_owner"
	appRole   = "tenants"
	testPass  = "test-only-password" // gitleaks:allow
)

//go:embed testdata/unprotected_tables.sql
var unprotectedTablesSQL string

type testDB struct {
	super  *pgx.Conn
	owner  *pgx.Conn
	name   string
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

// newTestDB creates the two roles like the CNPG cluster does and a fresh
// database owned by the owner role, then applies the migrations as the owner.
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

	db := fmt.Sprintf("tenants_test_%d", time.Now().UnixNano())
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
	return testDB{super: super, owner: owner, name: db, appDSN: dsnFor(t, base, appRole, db)}
}

func openApp(t *testing.T, db testDB) *Postgres {
	t.Helper()
	st, err := Open(context.Background(), db.appDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

func newTenant(id, name string) core.Tenant {
	return core.Tenant{ID: id, Name: name, AdminEmail: "admin@" + name + ".test", CreatedBy: "admin-1"}
}

const (
	idA = "11111111-1111-4111-8111-111111111111"
	idB = "22222222-2222-4222-8222-222222222222"
)

// Verifies: tenant-management AC-12; T-08
func TestMigrationsLeaveNoUnprotectedTables(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	got, err := UnprotectedTables(ctx, db.owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("unprotected tables after migrations: %v", got)
	}

	// The exemption is for the listed platform tables only.
	if _, err := db.owner.Exec(ctx, unprotectedTablesSQL); err != nil {
		t.Fatal(err)
	}
	got, err = UnprotectedTables(ctx, db.owner)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"public.no_rls_notes"}) {
		t.Fatalf("unprotected = %v, want [public.no_rls_notes]", got)
	}
}

// Verifies: tenant-management AC-12; T-08
func TestTenantsTableIsPlatformOnly(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	rows, err := db.owner.Query(ctx, `
		SELECT coalesce(r.rolname, 'PUBLIC'), acl.privilege_type
		FROM pg_class c, aclexplode(c.relacl) acl
		LEFT JOIN pg_roles r ON r.oid = acl.grantee
		WHERE c.oid = 'public.tenants'::regclass AND acl.grantee <> c.relowner
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (string, error) {
		var role, priv string
		err := r.Scan(&role, &priv)
		return role + ":" + priv, err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{appRole + ":INSERT", appRole + ":SELECT", appRole + ":UPDATE"}
	if !slices.Equal(got, want) {
		t.Fatalf("grants on tenants = %v, want %v", got, want)
	}

	// The application role cannot delete a row: deleted tenants stay as tombstones.
	st := openApp(t, db)
	if _, err := st.Create(ctx, newTenant(idA, "acme")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(ctx, `DELETE FROM tenants`); err == nil {
		t.Fatal("the application role could delete from tenants")
	}
}

// Verifies: tenant-management AC-12; T-08
func TestMigrationsRunAgainOnADatabaseWithData(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	st := openApp(t, db)
	if _, err := st.Create(ctx, newTenant(idA, "acme")); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, idA); err != nil {
		t.Fatalf("the tenant is gone after a second migration run: %v", err)
	}
}

// Verifies: tenant-management AC-5; T-05
func TestConcurrentRequestOnSameTenantGets409(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	st := openApp(t, db)

	unlockA, err := st.Lock(ctx, idA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Lock(ctx, idA); !errors.Is(err, core.ErrBusy) {
		t.Fatalf("second lock on A: err = %v, want ErrBusy", err)
	}
	unlockB, err := st.Lock(ctx, idB)
	if err != nil {
		t.Fatalf("lock on B while A is locked: %v", err)
	}
	unlockB()
	unlockA()
	unlockA2, err := st.Lock(ctx, idA)
	if err != nil {
		t.Fatalf("lock on A after unlock: %v", err)
	}
	unlockA2()

	// A process that dies while holding the lock: its connection ends and
	// the database releases the lock.
	other := openApp(t, db)
	unlockOther, err := other.Lock(ctx, idA)
	if err != nil {
		t.Fatal(err)
	}
	// Releases the dead connection back to the pool, so Close does not wait for it.
	defer unlockOther()
	if _, err := db.super.Exec(ctx, `
		SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype = 'advisory' AND classid = $1 AND database = (SELECT oid FROM pg_database WHERE datname = $2)`,
		lockClass, db.name); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		unlock, err := st.Lock(ctx, idA)
		if err == nil {
			unlock()
			break
		}
		if !errors.Is(err, core.ErrBusy) || time.Now().After(deadline) {
			t.Fatalf("lock not released after the holder died: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Verifies: tenant-management AC-8, AC-7
func TestNameIsUniqueAmongLiveTenants(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	st := openApp(t, db)

	a, err := st.Create(ctx, newTenant(idA, "Acme"))
	if err != nil {
		t.Fatal(err)
	}
	if a.State != core.StateProvisioning || len(a.Parts) != 0 {
		t.Fatalf("new tenant = %+v", a)
	}
	if _, err := st.Create(ctx, newTenant(idB, "acme")); !errors.Is(err, core.ErrNameTaken) {
		t.Fatalf("same name, other case: err = %v, want ErrNameTaken", err)
	}

	// A tombstone keeps no name, so the name is free again.
	now := time.Now()
	a.State, a.Parts, a.DeletedBy, a.DeletedAt = core.StateDeleted, []string{}, "admin-2", &now
	tomb, err := st.Save(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if tomb.Name != "" || tomb.AdminEmail != "" || tomb.DeletedBy != "admin-2" || tomb.DeletedAt == nil {
		t.Fatalf("tombstone = %+v", tomb)
	}
	if _, err := st.Create(ctx, newTenant(idB, "acme")); err != nil {
		t.Fatalf("name of a deleted tenant: %v", err)
	}

	live, err := st.List(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live[0].ID != idB {
		t.Fatalf("live tenants = %+v", live)
	}
	all, err := st.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all tenants = %d, want 2", len(all))
	}
	if _, err := st.Get(ctx, "33333333-3333-4333-8333-333333333333"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown tenant: err = %v", err)
	}
}
