package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/event"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/store"
	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/testpg"
)

func newEvent(tenant string, at time.Time) event.Event {
	return event.Event{
		ID: uuid.Must(uuid.NewV7()).String(), Time: at, TenantID: tenant, Metric: "api_calls", Quantity: 1,
		Attributes: json.RawMessage(`{"method":"GET","path":"/items","status":200}`),
	}
}

func openStore(t *testing.T, db testpg.DB) *store.Postgres {
	t.Helper()
	st, err := store.Open(context.Background(), db.WriterDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

// AC-11 (CI): the migrations leave no multi-tenant table without enabled and
// forced RLS, partitions included.
func TestMigrationsLeaveNoUnprotectedTables(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	// Partitions exist only once something is written.
	now := time.Now()
	if _, err := st.Write(context.Background(), []event.Event{newEvent("a", now), newEvent("a", now.Add(-48*time.Hour))}); err != nil {
		t.Fatal(err)
	}
	got, err := store.UnprotectedTables(context.Background(), db.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("tables with tenant_id but without enabled and forced RLS: %v", got)
	}
	var parts int
	if err := db.Owner.QueryRow(context.Background(),
		"SELECT count(*) FROM pg_inherits WHERE inhparent = 'usage_events'::regclass").Scan(&parts); err != nil {
		t.Fatal(err)
	}
	if parts < 2 {
		t.Fatalf("%d partitions, want at least 2 (two days written)", parts)
	}
}

// AC-11 negative fixture: the check reports a table without RLS, a table with
// RLS not forced, and an unprotected partition of a protected table.
func TestUnprotectedTablesFlagsTableWithoutRLS(t *testing.T) {
	db := testpg.New(t)
	fixture, err := os.ReadFile("testdata/unprotected_tables.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Owner.Exec(context.Background(), string(fixture)); err != nil {
		t.Fatal(err)
	}
	got, err := store.UnprotectedTables(context.Background(), db.Owner)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"public.no_rls_notes", "public.not_forced_notes", "public.parted_notes_2026"}
	if !slices.Equal(got, want) {
		t.Fatalf("UnprotectedTables = %v, want %v", got, want)
	}
}

// AC-11: usage_events has RLS enabled and forced, and Usage Writer's role does
// not own it and is NOSUPERUSER NOBYPASSRLS.
func TestUsageTablesHaveForcedRLSAndForeignOwner(t *testing.T) {
	db := testpg.New(t)
	var rls, force, super, bypass bool
	var owner string
	err := db.Owner.QueryRow(context.Background(), `
		SELECT c.relrowsecurity, c.relforcerowsecurity, pg_get_userbyid(c.relowner), r.rolsuper, r.rolbypassrls
		FROM pg_class c, pg_roles r
		WHERE c.oid = 'usage_events'::regclass AND r.rolname = $1`, testpg.WriterRole).Scan(&rls, &force, &owner, &super, &bypass)
	if err != nil {
		t.Fatal(err)
	}
	if !rls || !force || owner != testpg.OwnerRole || super || bypass {
		t.Fatalf("rls=%v force=%v owner=%s writer super=%v bypassrls=%v", rls, force, owner, super, bypass)
	}
}

// AC-11: without app.current_tenant_id Usage Writer's role sees no rows and
// cannot insert; it cannot read a partition directly either.
func TestNoTenantContextFailsClosed(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := st.Write(ctx, []event.Event{newEvent("tenant-a", now)}); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, db.WriterDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM usage_events").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("SELECT without tenant context returned %d rows, want 0", n)
	}
	_, err = conn.Exec(ctx, `INSERT INTO usage_events (event_id, event_time, tenant_id, metric, quantity)
		VALUES ($1, now(), 'tenant-a', 'api_calls', 1)`, uuid.NewString())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("INSERT without tenant context: err = %v, want an RLS violation (42501)", err)
	}
	_, err = conn.Exec(ctx, "SELECT count(*) FROM usage_events_"+now.Format("20060102"))
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("direct SELECT on a partition: err = %v, want permission denied (42501)", err)
	}
}

// AC-12: a batch that mixes tenants stores every row under its own tenant, and
// a tenant's session sees only its own rows.
func TestMixedBatchKeepsTenantsApart(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	ctx := context.Background()
	now := time.Now()
	batch := []event.Event{newEvent("tenant-a", now), newEvent("tenant-b", now), newEvent("tenant-a", now), newEvent("tenant-b", now)}
	fresh, err := st.Write(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != len(batch) {
		t.Fatalf("%d new events, want %d", len(fresh), len(batch))
	}
	for _, e := range batch {
		var tenant string
		if err := db.Super.QueryRow(ctx, "SELECT tenant_id FROM usage_events WHERE event_id = $1", e.ID).Scan(&tenant); err != nil {
			t.Fatal(err)
		}
		if tenant != e.TenantID {
			t.Fatalf("event %s stored under %q, want %q", e.ID, tenant, e.TenantID)
		}
	}

	conn, err := pgx.Connect(ctx, db.WriterDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', 'tenant-a', true)"); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT DISTINCT tenant_id FROM usage_events")
		if err != nil {
			return err
		}
		tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		if !slices.Equal(tenants, []string{"tenant-a"}) {
			t.Errorf("tenant-a's session sees tenants %v, want only tenant-a", tenants)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// AC-9 at the store level: a repeated event, in a later batch or in the same
// one, is stored once and is not reported as new again.
func TestWriteStoresEachEventOnce(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	ctx := context.Background()
	e := newEvent("tenant-a", time.Now())
	other := newEvent("tenant-a", time.Now())

	fresh, err := st.Write(ctx, []event.Event{e, e})
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 {
		t.Fatalf("first batch: %d new events, want 1", len(fresh))
	}
	fresh, err = st.Write(ctx, []event.Event{e, other})
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 || fresh[0].ID != other.ID {
		t.Fatalf("second batch: new events %v, want only %s", fresh, other.ID)
	}
	if n := db.Count(t, []string{e.ID, other.ID}); n != 2 {
		t.Fatalf("%d rows, want 2", n)
	}
}

// The attributes are stored as given, for the api_calls fields of AC-1.
func TestWriteKeepsAttributes(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	e := newEvent("tenant-a", time.Now())
	if _, err := st.Write(context.Background(), []event.Event{e}); err != nil {
		t.Fatal(err)
	}
	var path string
	var status int
	if err := db.Super.QueryRow(context.Background(),
		"SELECT attributes->>'path', (attributes->>'status')::int FROM usage_events WHERE event_id = $1", e.ID).Scan(&path, &status); err != nil {
		t.Fatal(err)
	}
	if path != "/items" || status != 200 {
		t.Fatalf("attributes path=%q status=%d", path, status)
	}
}
