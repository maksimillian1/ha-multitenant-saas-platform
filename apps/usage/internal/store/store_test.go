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

const day = 24 * time.Hour

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

func write(t *testing.T, st *store.Postgres, events ...event.Event) []event.Event {
	t.Helper()
	fresh, err := st.Write(context.Background(), events)
	if err != nil {
		t.Fatal(err)
	}
	return fresh
}

// runJob runs the TimescaleDB policy job proc of the hypertable or continuous
// aggregate rel now, as the owner, instead of waiting for its schedule.
func runJob(t *testing.T, db testpg.DB, proc, rel string) {
	t.Helper()
	ctx := context.Background()
	var id int
	err := db.Owner.QueryRow(ctx, `
		SELECT j.job_id FROM timescaledb_information.jobs j
		LEFT JOIN timescaledb_information.continuous_aggregates c
		       ON c.materialization_hypertable_name = j.hypertable_name
		WHERE j.proc_name = $1 AND coalesce(c.view_name, j.hypertable_name) = $2`, proc, rel).Scan(&id)
	if err != nil {
		t.Fatalf("job %s on %s: %v", proc, rel, err)
	}
	if _, err := db.Owner.Exec(ctx, "CALL run_job($1)", id); err != nil {
		t.Fatalf("run job %s on %s: %v", proc, rel, err)
	}
}

func compress(t *testing.T, db testpg.DB) { runJob(t, db, "policy_compression", "usage_events") }
func refreshHourly(t *testing.T, db testpg.DB) {
	runJob(t, db, "policy_refresh_continuous_aggregate", "usage_hourly")
}

// compressed reports whether the chunk holding at is in the columnstore.
func compressed(t *testing.T, db testpg.DB, at time.Time) bool {
	t.Helper()
	var c bool
	if err := db.Owner.QueryRow(context.Background(), `
		SELECT is_compressed FROM timescaledb_information.chunks
		WHERE hypertable_name = 'usage_events' AND range_start <= $1 AND range_end > $1`, at).Scan(&c); err != nil {
		t.Fatalf("chunk of %s: %v", at, err)
	}
	return c
}

func count(t *testing.T, db testpg.DB, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Super.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// asRole runs fn in a transaction as role, with tenant as app.current_tenant_id
// unless it is empty. The superuser connection drops its rights with SET ROLE.
func asRole(t *testing.T, db testpg.DB, role, tenant string, fn func(tx pgx.Tx) error) error {
	t.Helper()
	ctx := context.Background()
	return pgx.BeginFunc(ctx, db.Super, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+role); err != nil {
			return err
		}
		if tenant != "" {
			if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenant); err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

func isPermissionDenied(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}

// AC-5, AC-10 (CI): with raw, compressed and aggregated data in place, no
// relation with tenant_id is open to an application role without isolation.
func TestMigrationsLeaveNoUnprotectedTables(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	now := time.Now()
	write(t, st, newEvent("a", now), newEvent("a", now.Add(-10*day)))
	compress(t, db)
	refreshHourly(t, db)
	if !compressed(t, db, now.Add(-10*day)) {
		t.Fatal("the 10-day-old chunk is not compressed, so the check covers no compressed chunk")
	}
	got, err := store.UnprotectedTables(context.Background(), db.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("relations with tenant_id open without tenant isolation: %v", got)
	}
}

// AC-10 negative fixture: the check reports granted tables without forced RLS
// and granted views that do not filter by tenant behind a security barrier.
func TestUnprotectedTablesFlagsGrantedTableWithoutRLS(t *testing.T) {
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
	want := []string{"public.all_notes", "public.leaky_notes", "public.no_rls_notes", "public.not_forced_notes"}
	if !slices.Equal(got, want) {
		t.Fatalf("UnprotectedTables = %v, want %v", got, want)
	}
}

// AC-5: usage_events is a hypertable with 1-day chunks, keyed by the dedup key
// of ADR-0007, owned by the owner role; Usage Writer's role is not privileged.
func TestUsageEventsIsHypertableWithDayChunks(t *testing.T) {
	db := testpg.New(t)
	ctx := context.Background()
	var interval time.Duration
	var owner, key string
	var super, bypass bool
	err := db.Owner.QueryRow(ctx, `
		SELECT d.time_interval, h.owner,
		       (SELECT string_agg(a.attname, ',' ORDER BY k.ord)
		        FROM pg_constraint c, unnest(c.conkey) WITH ORDINALITY k(attnum, ord)
		        JOIN pg_attribute a ON a.attrelid = 'usage_events'::regclass AND a.attnum = k.attnum
		        WHERE c.conrelid = 'usage_events'::regclass AND c.contype = 'p'),
		       r.rolsuper, r.rolbypassrls
		FROM timescaledb_information.hypertables h
		JOIN timescaledb_information.dimensions d ON d.hypertable_name = h.hypertable_name
		JOIN pg_roles r ON r.rolname = $1
		WHERE h.hypertable_name = 'usage_events' AND d.column_name = 'event_time'`, testpg.WriterRole,
	).Scan(&interval, &owner, &key, &super, &bypass)
	if err != nil {
		t.Fatal(err)
	}
	if interval != day || owner != testpg.OwnerRole || key != "tenant_id,event_id,event_time" || super || bypass {
		t.Fatalf("chunk interval=%s owner=%s key=%s writer super=%v bypassrls=%v", interval, owner, key, super, bypass)
	}
}

// AC-5: write_usage_events rejects a batch without tenant context or with an
// event of another tenant, and stores nothing of it.
func TestWriteRejectsOtherTenant(t *testing.T) {
	db := testpg.New(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, db.WriterDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	call := func(tenant string, events ...event.Event) error {
		return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if tenant != "" {
				if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenant); err != nil {
					return err
				}
			}
			var ids, tenants, metrics, attrs []string
			var times []time.Time
			var quantities []int64
			for _, e := range events {
				ids, times, tenants = append(ids, e.ID), append(times, e.Time), append(tenants, e.TenantID)
				metrics, quantities, attrs = append(metrics, e.Metric), append(quantities, e.Quantity), append(attrs, "{}")
			}
			_, err := tx.Exec(ctx, `SELECT * FROM write_usage_events($1::uuid[], $2::timestamptz[], $3::text[], $4::text[], $5::bigint[], $6::text[])`,
				ids, times, tenants, metrics, quantities, attrs)
			return err
		})
	}
	a, b := newEvent("tenant-a", time.Now()), newEvent("tenant-b", time.Now())
	if err := call("", a); !isPermissionDenied(err) {
		t.Fatalf("batch without tenant context: err = %v, want 42501", err)
	}
	if err := call("tenant-a", a, b); !isPermissionDenied(err) {
		t.Fatalf("batch of tenant-a with an event of tenant-b: err = %v, want 42501", err)
	}
	if n := db.Count(t, []string{a.ID, b.ID}); n != 0 {
		t.Fatalf("%d rows stored from rejected batches, want 0", n)
	}
	if err := call("tenant-a", a); err != nil {
		t.Fatalf("own batch: %v", err)
	}
}

// AC-5, AC-10: Usage Writer's and the reader's roles cannot touch any table
// directly: not the hypertable, its chunks, compressed chunks, the aggregate
// or its materialization table.
func TestWriterRoleHasNoDirectAccess(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	ctx := context.Background()
	now := time.Now()
	write(t, st, newEvent("a", now), newEvent("a", now.Add(-10*day)))
	compress(t, db)
	refreshHourly(t, db)

	rels := []string{"usage_events", "usage_hourly"}
	rows, err := db.Owner.Query(ctx, `
		SELECT format('%I.%I', chunk_schema, chunk_name) FROM timescaledb_information.chunks WHERE hypertable_name = 'usage_events'
		UNION ALL
		SELECT format('%I.%I', s.schema_name, s.table_name) FROM _timescaledb_catalog.chunk c
		JOIN _timescaledb_catalog.chunk s ON s.id = c.compressed_chunk_id
		UNION ALL
		SELECT format('%I.%I', materialization_hypertable_schema, materialization_hypertable_name)
		FROM timescaledb_information.continuous_aggregates WHERE view_name = 'usage_hourly'`)
	if err != nil {
		t.Fatal(err)
	}
	internal, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(internal) < 4 { // 2 chunks, 1 compressed chunk, 1 materialization table
		t.Fatalf("internal relations %v: want chunks, a compressed chunk and the materialization table", internal)
	}
	rels = append(rels, internal...)

	for _, role := range []string{testpg.WriterRole, testpg.ReaderRole} {
		for _, rel := range rels {
			err := asRole(t, db, role, "a", func(tx pgx.Tx) error {
				_, err := tx.Exec(ctx, "SELECT count(*) FROM "+rel)
				return err
			})
			if !isPermissionDenied(err) {
				t.Errorf("%s: SELECT on %s: err = %v, want permission denied", role, rel, err)
			}
		}
		err := asRole(t, db, role, "a", func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO usage_events (event_id, event_time, tenant_id, metric, quantity)
				VALUES (gen_random_uuid(), now(), 'a', 'api_calls', 1)`)
			return err
		})
		if !isPermissionDenied(err) {
			t.Errorf("%s: INSERT on usage_events: err = %v, want permission denied", role, err)
		}
	}
}

// AC-10: the tenant views return only the session's tenant, and nothing
// without a tenant.
func TestTenantViewsShowOnlyCurrentTenant(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	ctx := context.Background()
	at := time.Now().Add(-2 * day)
	write(t, st, newEvent("tenant-a", at), newEvent("tenant-b", at), newEvent("tenant-a", at), newEvent("tenant-b", at))
	refreshHourly(t, db)

	for _, view := range []string{"usage_events_tenant", "usage_hourly_tenant"} {
		for tenant, want := range map[string][]string{"tenant-a": {"tenant-a"}, "tenant-b": {"tenant-b"}, "": nil} {
			var got []string
			err := asRole(t, db, testpg.ReaderRole, tenant, func(tx pgx.Tx) error {
				rows, err := tx.Query(ctx, "SELECT DISTINCT tenant_id FROM "+view)
				if err != nil {
					return err
				}
				got, err = pgx.CollectRows(rows, pgx.RowTo[string])
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s as tenant %q: tenants %v, want %v", view, tenant, got, want)
			}
		}
	}
}

// AC-6: the columnstore policy compresses chunks older than 8 days and leaves
// younger ones uncompressed.
func TestColumnstorePolicyCompressesOnlyOlderThan8Days(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	now := time.Now()
	old, young := now.Add(-10*day), now.Add(-6*day)
	write(t, st, newEvent("a", old), newEvent("a", young), newEvent("a", now))
	compress(t, db)
	if !compressed(t, db, old) {
		t.Error("the 10-day-old chunk is not compressed")
	}
	if compressed(t, db, young) || compressed(t, db, now) {
		t.Error("a chunk younger than 8 days is compressed")
	}
	var segmentby, orderby string
	if err := db.Owner.QueryRow(context.Background(), `
		SELECT string_agg(attname, ',' ORDER BY segmentby_column_index) FILTER (WHERE segmentby_column_index IS NOT NULL),
		       string_agg(attname, ',' ORDER BY orderby_column_index) FILTER (WHERE orderby_column_index IS NOT NULL)
		FROM timescaledb_information.compression_settings WHERE hypertable_name = 'usage_events'`).Scan(&segmentby, &orderby); err != nil {
		t.Fatal(err)
	}
	if segmentby != "tenant_id" || orderby != "event_time,event_id" {
		t.Fatalf("segmentby=%q orderby=%q, want tenant_id and event_time,event_id", segmentby, orderby)
	}
}

// AC-7, QS-06: an event sent again after its original was compressed is not
// stored twice and is not reported as new.
func TestDuplicateIntoCompressedChunkIsStoredOnce(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	e := newEvent("a", time.Now().Add(-10*day))
	write(t, st, e, newEvent("a", e.Time.Add(time.Second)))
	compress(t, db)
	if !compressed(t, db, e.Time) {
		t.Fatal("the original's chunk is not compressed")
	}
	if fresh := write(t, st, e); len(fresh) != 0 {
		t.Fatalf("the duplicate was reported as new: %v", fresh)
	}
	if n := db.Count(t, []string{e.ID}); n != 1 {
		t.Fatalf("%d rows for the event, want 1", n)
	}
}

// AC-8: retention drops raw chunks and aggregate rows older than 13 months and
// keeps younger ones.
func TestRetentionDropsOnlyOlderThan13Months(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	now := time.Now()
	expired, kept := now.AddDate(0, -14, 0), now.AddDate(0, -12, 0)
	e, k := newEvent("a", expired), newEvent("a", kept)
	write(t, st, e, k)
	refreshHourly(t, db)
	hours := func() int {
		return count(t, db, "SELECT count(*) FROM usage_hourly WHERE hour IN (date_trunc('hour', $1::timestamptz), date_trunc('hour', $2::timestamptz))", expired, kept)
	}
	if n := hours(); n != 2 {
		t.Fatalf("%d aggregate rows before retention, want 2", n)
	}
	runJob(t, db, "policy_retention", "usage_events")
	runJob(t, db, "policy_retention", "usage_hourly")
	if n := db.Count(t, []string{e.ID}); n != 0 {
		t.Error("the 14-month-old event was not dropped")
	}
	if n := db.Count(t, []string{k.ID}); n != 1 {
		t.Error("the 12-month-old event was dropped")
	}
	if n := count(t, db, "SELECT count(*) FROM usage_hourly WHERE hour = date_trunc('hour', $1::timestamptz)", kept); n != 1 {
		t.Error("the 12-month-old aggregate row was dropped")
	}
	if n := hours(); n != 1 {
		t.Errorf("%d aggregate rows after retention, want 1", n)
	}
}

// AC-9: after a refresh the hourly aggregate holds the sum and count of the
// hour, including an event that arrived late into a compressed chunk.
func TestHourlyAggregateIncludesLateEvent(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	hour := time.Now().Add(-10 * day).Truncate(time.Hour)
	first, second := newEvent("a", hour.Add(time.Minute)), newEvent("a", hour.Add(2*time.Minute))
	second.Quantity = 4
	write(t, st, first, second)
	compress(t, db)
	refreshHourly(t, db)
	sum := func() (q, n int) {
		if err := db.Super.QueryRow(context.Background(),
			"SELECT quantity, events FROM usage_hourly WHERE tenant_id = 'a' AND metric = 'api_calls' AND hour = $1", hour).Scan(&q, &n); err != nil {
			t.Fatal(err)
		}
		return q, n
	}
	if q, n := sum(); q != 5 || n != 2 {
		t.Fatalf("hour before the late event: quantity=%d events=%d, want 5 and 2", q, n)
	}
	late := newEvent("a", hour.Add(30*time.Minute))
	late.Quantity = 10
	if !compressed(t, db, late.Time) {
		t.Fatal("the late event's chunk is not compressed")
	}
	write(t, st, late)
	refreshHourly(t, db)
	if q, n := sum(); q != 15 || n != 3 {
		t.Fatalf("hour after the late event: quantity=%d events=%d, want 15 and 3", q, n)
	}
}

// AC-11, QS-18: deleting one tenant's events removes them from compressed and
// uncompressed chunks and, after a refresh, from the aggregate; other tenants
// keep theirs.
func TestDeleteTenantFromCompressedChunks(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	ctx := context.Background()
	now := time.Now()
	for _, at := range []time.Time{now.Add(-10 * day), now.Add(-2 * day)} {
		write(t, st, newEvent("a", at), newEvent("b", at), newEvent("b", at))
	}
	compress(t, db)
	refreshHourly(t, db)
	if !compressed(t, db, now.Add(-10*day)) {
		t.Fatal("the 10-day-old chunk is not compressed")
	}

	if _, err := db.Owner.Exec(ctx, "DELETE FROM usage_events WHERE tenant_id = 'b'"); err != nil {
		t.Fatal(err)
	}
	refreshHourly(t, db)
	if n := count(t, db, "SELECT count(*) FROM usage_events WHERE tenant_id = 'b'"); n != 0 {
		t.Errorf("%d raw events of tenant b left", n)
	}
	if n := count(t, db, "SELECT count(*) FROM usage_hourly WHERE tenant_id = 'b'"); n != 0 {
		t.Errorf("%d aggregate rows of tenant b left", n)
	}
	if n := count(t, db, "SELECT count(*) FROM usage_events WHERE tenant_id = 'a'"); n != 2 {
		t.Errorf("%d raw events of tenant a, want 2", n)
	}
	if n := count(t, db, "SELECT coalesce(sum(events), 0) FROM usage_hourly WHERE tenant_id = 'a'"); n != 2 {
		t.Errorf("aggregate counts %d events of tenant a, want 2", n)
	}
}

// AC-12 of HAMSP-26: a batch that mixes tenants stores every row under its own
// tenant.
func TestMixedBatchKeepsTenantsApart(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	ctx := context.Background()
	now := time.Now()
	batch := []event.Event{newEvent("tenant-a", now), newEvent("tenant-b", now), newEvent("tenant-a", now), newEvent("tenant-b", now)}
	if fresh := write(t, st, batch...); len(fresh) != len(batch) {
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
}

// AC-9 of HAMSP-26 at the store level: a repeated event, in a later batch or
// in the same one, is stored once and is not reported as new again.
func TestWriteStoresEachEventOnce(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	e := newEvent("tenant-a", time.Now())
	other := newEvent("tenant-a", time.Now())

	if fresh := write(t, st, e, e); len(fresh) != 1 {
		t.Fatalf("first batch: %d new events, want 1", len(fresh))
	}
	if fresh := write(t, st, e, other); len(fresh) != 1 || fresh[0].ID != other.ID {
		t.Fatalf("second batch: new events %v, want only %s", fresh, other.ID)
	}
	if n := db.Count(t, []string{e.ID, other.ID}); n != 2 {
		t.Fatalf("%d rows, want 2", n)
	}
}

// The attributes are stored as given, for the api_calls fields of HAMSP-26 AC-1.
func TestWriteKeepsAttributes(t *testing.T) {
	db := testpg.New(t)
	st := openStore(t, db)
	e := newEvent("tenant-a", time.Now())
	write(t, st, e)
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
