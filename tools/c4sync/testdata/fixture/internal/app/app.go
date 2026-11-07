// Package app records events: it saves them and publishes them.
package app

import (
	"context"

	"example.com/fixture/internal/app/events"
	"example.com/fixture/internal/bus"
)

// EventStore is implemented in another component (store), which does not import app.
type EventStore interface {
	Save(ctx context.Context, e events.Event) error
}

// Service uses a store behind an interface and a bus client directly.
type Service struct {
	Store EventStore
	Bus   *bus.Client
}

// Record saves and publishes an event.
func (s *Service) Record(ctx context.Context, e events.Event) error {
	if err := s.Store.Save(ctx, e); err != nil {
		return err
	}
	return s.Bus.Publish(bus.TopicEvents, e)
}

// Page is generic.
type Page[T any] struct{ Items []T }

type base struct{}

// Admin embeds an unexported struct.
type Admin struct{ base }
