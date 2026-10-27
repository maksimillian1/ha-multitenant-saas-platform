// Package store is the usage store of ADR-0006: the only way into the usage
// tables. They live in a TimescaleDB hypertable on the usage cluster
// (ADR-0008), which Usage Writer reaches only through write_usage_events. The
// read side follows HAMSP-9.
package store

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/core"
)

// Postgres is the TimescaleDB store of ADR-0008.
type Postgres struct {
	pool *pgxpool.Pool
}

var _ core.EventWriter = (*Postgres)(nil)

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
	return &Postgres{pool: pool}, nil
}

func (s *Postgres) Close() { s.pool.Close() }

func (s *Postgres) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// writeEvents stores one tenant's events and returns the new ones. The function
// checks every event against app.current_tenant_id (ADR-0008).
const writeEvents = `
SELECT event_id::text, event_time
FROM write_usage_events($1::uuid[], $2::timestamptz[], $3::text[], $4::text[], $5::bigint[], $6::text[])`

type key struct {
	id string
	t  int64
}

func (s *Postgres) Write(ctx context.Context, events []core.Event) ([]core.Event, error) {
	if len(events) == 0 {
		return nil, nil
	}
	byTenant := map[string][]core.Event{}
	var order []string
	for _, e := range events {
		if _, ok := byTenant[e.TenantID]; !ok {
			order = append(order, e.TenantID)
		}
		byTenant[e.TenantID] = append(byTenant[e.TenantID], e)
	}
	var fresh []core.Event
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		fresh = fresh[:0]
		for _, tenant := range order {
			group := byTenant[tenant]
			// set_config(..., true) is SET LOCAL: write_usage_events accepts only this tenant's events.
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
			rows, err := tx.Query(ctx, writeEvents, ids, times, tenants, metrics, quantities, attrs)
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

// Connect retries until ctx is done, because on a fresh cluster the database
// may still be starting when the migration job runs.
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
