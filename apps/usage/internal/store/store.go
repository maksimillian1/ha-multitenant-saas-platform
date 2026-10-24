// Package store is the usage store of ADR-0006: the only way into the usage
// tables. v1 keeps them in their own database on the main PostgreSQL cluster;
// another store implements the same interface later. The read side follows
// HAMSP-9.
package store

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/event"
)

// Writer stores usage events once (ADR-0007).
type Writer interface {
	// Write stores a batch in one transaction and returns the events that were
	// new. Events stored before, in this batch or an earlier one, are skipped.
	Write(ctx context.Context, events []event.Event) ([]event.Event, error)
}

// Postgres is the v1 store.
type Postgres struct {
	pool *pgxpool.Pool

	mu    sync.Mutex
	parts map[string]bool // UTC days known to have a partition
}

var _ Writer = (*Postgres)(nil)

// Open connects as Usage Writer's role. Statements use the extended protocol
// with unnamed statements, which PgBouncer in transaction mode supports.
func Open(ctx context.Context, dsn string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Postgres{pool: pool, parts: map[string]bool{}}, nil
}

func (s *Postgres) Close() { s.pool.Close() }

func (s *Postgres) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func day(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// EnsurePartitions creates the day partitions from..to (UTC) that are missing.
// It runs in its own short transaction, so the lock that creating a partition
// takes on the table is not held while a batch is inserted.
func (s *Postgres) EnsurePartitions(ctx context.Context, from, to time.Time) error {
	missing := false
	s.mu.Lock()
	for d := from.UTC().Truncate(24 * time.Hour); !d.After(to.UTC()); d = d.Add(24 * time.Hour) {
		if !s.parts[day(d)] {
			missing = true
			break
		}
	}
	s.mu.Unlock()
	if !missing {
		return nil
	}
	if _, err := s.pool.Exec(ctx, "SELECT ensure_usage_partitions($1::date, $2::date)", day(from), day(to)); err != nil {
		return err
	}
	s.mu.Lock()
	for d := from.UTC().Truncate(24 * time.Hour); !d.After(to.UTC()); d = d.Add(24 * time.Hour) {
		s.parts[day(d)] = true
	}
	s.mu.Unlock()
	return nil
}

const insertEvents = `
INSERT INTO usage_events (event_id, event_time, tenant_id, metric, quantity, attributes)
SELECT id, t, tenant, metric, quantity, attrs::jsonb
FROM unnest($1::uuid[], $2::timestamptz[], $3::text[], $4::text[], $5::bigint[], $6::text[])
     AS u(id, t, tenant, metric, quantity, attrs)
ON CONFLICT (tenant_id, event_id, event_time) DO NOTHING
RETURNING event_id::text, event_time`

type key struct {
	id string
	t  int64
}

func (s *Postgres) Write(ctx context.Context, events []event.Event) ([]event.Event, error) {
	if len(events) == 0 {
		return nil, nil
	}
	from, to := events[0].Time, events[0].Time
	byTenant := map[string][]event.Event{}
	var order []string
	for _, e := range events {
		if e.Time.Before(from) {
			from = e.Time
		}
		if e.Time.After(to) {
			to = e.Time
		}
		if _, ok := byTenant[e.TenantID]; !ok {
			order = append(order, e.TenantID)
		}
		byTenant[e.TenantID] = append(byTenant[e.TenantID], e)
	}
	if err := s.EnsurePartitions(ctx, from, to); err != nil {
		return nil, err
	}

	var fresh []event.Event
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		fresh = fresh[:0]
		for _, tenant := range order {
			group := byTenant[tenant]
			// set_config(..., true) is SET LOCAL: RLS admits only this tenant's rows.
			if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenant); err != nil {
				return err
			}
			ids := make([]string, len(group))
			times := make([]time.Time, len(group))
			tenants := make([]string, len(group))
			metrics := make([]string, len(group))
			quantities := make([]int64, len(group))
			attrs := make([]string, len(group))
			for i, e := range group {
				ids[i], times[i], tenants[i], metrics[i], quantities[i] = e.ID, e.Time, e.TenantID, e.Metric, e.Quantity
				attrs[i] = "{}"
				if len(e.Attributes) > 0 {
					attrs[i] = string(e.Attributes)
				}
			}
			rows, err := tx.Query(ctx, insertEvents, ids, times, tenants, metrics, quantities, attrs)
			if err != nil {
				return err
			}
			inserted := map[key]bool{}
			for rows.Next() {
				var id string
				var t time.Time
				if err := rows.Scan(&id, &t); err != nil {
					rows.Close()
					return err
				}
				inserted[key{id, t.UnixMicro()}] = true
			}
			if err := rows.Err(); err != nil {
				return err
			}
			for _, e := range group {
				k := key{e.ID, e.Time.UnixMicro()}
				if inserted[k] {
					fresh = append(fresh, e)
					delete(inserted, k) // a duplicate inside the batch is not new twice
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return fresh, nil
}
