// Package store implements app.EventStore without importing app.
package store

import (
	"context"
	"database/sql"

	"example.com/fixture/internal/app/events"
)

const insertEvent = "INSERT INTO events (id) VALUES ($1)"

// SQLStore keeps events in SQL.
type SQLStore struct{ db *sql.DB }

// Save writes an event.
func (s *SQLStore) Save(ctx context.Context, e events.Event) error {
	_, err := s.db.ExecContext(ctx, insertEvent, e.ID)
	return err
}

// Count reads two tables.
func (s *SQLStore) Count(ctx context.Context) (n int, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM events JOIN tenants t ON true").Scan(&n)
	return n, err
}
