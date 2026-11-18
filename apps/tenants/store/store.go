// Package store keeps the tenant registry (ADR-0012, item 2).
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
)

// lockClass is the first key of the advisory locks on tenants, so they do not
// collide with other advisory locks of the database.
const lockClass = 51

type Postgres struct {
	pool *pgxpool.Pool
}

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

const columns = `tenant_id, coalesce(name, ''), coalesce(admin_email, ''), state, parts,
	coalesce(last_error, ''), created_by, created_at, updated_at, coalesce(deleted_by, ''), deleted_at`

func scan(row pgx.Row) (core.Tenant, error) {
	var t core.Tenant
	var state string
	err := row.Scan(&t.ID, &t.Name, &t.AdminEmail, &state, &t.Parts, &t.LastError,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &t.DeletedBy, &t.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Tenant{}, core.ErrNotFound
	}
	t.State = core.State(state)
	if t.Parts == nil {
		t.Parts = []string{}
	}
	return t, err
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Create inserts a new tenant in state provisioning.
func (s *Postgres) Create(ctx context.Context, t core.Tenant) (core.Tenant, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO tenants (tenant_id, name, admin_email, state, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+columns,
		t.ID, t.Name, t.AdminEmail, string(core.StateProvisioning), t.CreatedBy)
	created, err := scan(row)
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "tenants_live_name_idx" {
			return core.Tenant{}, core.ErrNameTaken
		}
	}
	return created, err
}

func (s *Postgres) Get(ctx context.Context, id string) (core.Tenant, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM tenants WHERE tenant_id = $1`, id))
}

// List returns the tenants, newest first; deleted ones only if asked for.
func (s *Postgres) List(ctx context.Context, includeDeleted bool) ([]core.Tenant, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+columns+` FROM tenants
		WHERE $1 OR state <> 'deleted'
		ORDER BY created_at DESC, tenant_id`, includeDeleted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []core.Tenant{}
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

// Save writes the tenant's state, parts, last error and, for a tombstone,
// clears its name and admin email.
func (s *Postgres) Save(ctx context.Context, t core.Tenant) (core.Tenant, error) {
	return scan(s.pool.QueryRow(ctx, `
		UPDATE tenants SET
			state = $2, parts = $3, last_error = $4,
			name = CASE WHEN $2 = 'deleted' THEN NULL ELSE name END,
			admin_email = CASE WHEN $2 = 'deleted' THEN NULL ELSE admin_email END,
			deleted_by = $5, deleted_at = $6, updated_at = now()
		WHERE tenant_id = $1
		RETURNING `+columns,
		t.ID, string(t.State), t.Parts, nullIfEmpty(t.LastError), nullIfEmpty(t.DeletedBy), t.DeletedAt))
}

// Lock takes the tenant's lock for one request: a session advisory lock on a
// connection held until unlock. A second request on the same tenant gets
// core.ErrBusy at once. When the process dies, its connection closes and the
// database releases the lock.
func (s *Postgres) Lock(ctx context.Context, id string) (unlock func(), err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1, hashtext($2))`, lockClass, id).Scan(&got); err != nil {
		conn.Release()
		return nil, err
	}
	if !got {
		conn.Release()
		return nil, core.ErrBusy
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1, hashtext($2))`, lockClass, id); err != nil {
			// The lock may still be held: drop the connection so it is released.
			_ = conn.Conn().Close(ctx)
		}
		conn.Release()
	}, nil
}
