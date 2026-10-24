// Package store keeps ExampleApp items in PostgreSQL. Every query runs inside a
// transaction that first sets app.current_tenant_id, so RLS limits it to one
// tenant. This works with PgBouncer in transaction mode because the setting is
// local to the transaction.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when the item does not exist or belongs to another tenant.
var ErrNotFound = errors.New("item not found")

type Item struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	pool *pgxpool.Pool
}

// Open connects to the database as the application role. Statements use the
// extended protocol with unnamed statements, which PgBouncer in transaction
// mode supports.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) inTenantTx(ctx context.Context, tenantID string, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// set_config(..., true) is SET LOCAL with a bind parameter.
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID); err != nil {
			return err
		}
		return fn(tx)
	})
}

const itemColumns = "id::text, tenant_id, name, created_at"

func scanItem(row pgx.Row) (Item, error) {
	var it Item
	err := row.Scan(&it.ID, &it.TenantID, &it.Name, &it.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	return it, err
}

func (s *Store) Create(ctx context.Context, tenantID, name string) (it Item, err error) {
	err = s.inTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		it, err = scanItem(tx.QueryRow(ctx,
			"INSERT INTO items (tenant_id, name) VALUES ($1, $2) RETURNING "+itemColumns, tenantID, name))
		return err
	})
	return it, err
}

func (s *Store) List(ctx context.Context, tenantID string) (items []Item, err error) {
	err = s.inTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT "+itemColumns+" FROM items ORDER BY created_at, id")
		if err != nil {
			return err
		}
		items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Item, error) { return scanItem(r) })
		return err
	})
	return items, err
}

func (s *Store) Get(ctx context.Context, tenantID, id string) (it Item, err error) {
	err = s.inTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		it, err = scanItem(tx.QueryRow(ctx, "SELECT "+itemColumns+" FROM items WHERE id = $1", id))
		return err
	})
	return it, err
}

func (s *Store) Update(ctx context.Context, tenantID, id, name string) (it Item, err error) {
	err = s.inTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		it, err = scanItem(tx.QueryRow(ctx,
			"UPDATE items SET name = $2 WHERE id = $1 RETURNING "+itemColumns, id, name))
		return err
	})
	return it, err
}

func (s *Store) Delete(ctx context.Context, tenantID, id string) error {
	return s.inTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "DELETE FROM items WHERE id = $1", id)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}
