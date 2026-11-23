// Package store is the only way into the usage tables (ADR-0006, ADR-0008).
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/core"
)

type Postgres struct {
	pool *pgxpool.Pool
}

var _ core.EventWriter = (*Postgres)(nil)

// Unnamed statements of the extended protocol, which PgBouncer in transaction mode supports.
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

// write_usage_events checks every event against app.current_tenant_id (ADR-0008).
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
		for _, tenant := range order {
			group := byTenant[tenant]
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
		return nil, classify(err)
	}
	return fresh, nil
}

// Classes 22 and 23 come from the events and fail on every retry; anything else may pass.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (strings.HasPrefix(pgErr.Code, "22") || strings.HasPrefix(pgErr.Code, "23")) {
		return fmt.Errorf("%w: %w", core.ErrBadData, err)
	}
	return err
}
