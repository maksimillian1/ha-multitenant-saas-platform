// Package events is part of the app component (internal/app/**).
package events

// Event is what the service records.
type Event struct{ ID ID }

// ID is an alias: aliases are not code elements.
type ID = string
