// Package core is linked into every binary: stdlib and uuid only (.golangci.yml).
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

var Metrics = map[string]bool{
	"api_calls": true,
}

const (
	MaxBatch      = 1000
	MaxAttributes = 4 << 10
	MaxAge        = 7 * 24 * time.Hour
	MaxClockSkew  = 5 * time.Minute
)

var tenantRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type Event struct {
	ID         string          `json:"event_id"`
	Time       time.Time       `json:"event_time"`
	TenantID   string          `json:"tenant_id"`
	Metric     string          `json:"metric"`
	Quantity   int64           `json:"quantity"`
	Attributes json.RawMessage `json:"attributes,omitempty"`
}

// Usage Writer must declare a new version before writeapi writes it (ADR-0010).
const SchemaVersion = 1

// A record without schema_version predates versions and is version 1.
type Record struct {
	SchemaVersion int `json:"schema_version"`
	Event
}

func (e Event) Validate(now time.Time) error {
	if err := e.ValidateContent(); err != nil {
		return err
	}
	if e.Time.Before(now.Add(-MaxAge)) || e.Time.After(now.Add(MaxClockSkew)) {
		return fmt.Errorf("event_time %s is outside [now - %s, now + %s]", e.Time.Format(time.RFC3339), MaxAge, MaxClockSkew)
	}
	return nil
}

// ValidateContent skips the event_time window: accepted or replayed events may be old by store time.
func (e Event) ValidateContent() error {
	switch {
	case e.ID == "":
		return errors.New("event_id is required")
	case uuid.Validate(e.ID) != nil:
		return errors.New("event_id must be a UUID")
	case e.Time.IsZero():
		return errors.New("event_time is required")
	case e.TenantID == "":
		return errors.New("tenant_id is required")
	case !tenantRe.MatchString(e.TenantID):
		return errors.New("tenant_id is malformed")
	case e.Metric == "":
		return errors.New("metric is required")
	case !Metrics[e.Metric]:
		return fmt.Errorf("unknown metric %q", e.Metric)
	case e.Quantity <= 0:
		return errors.New("quantity must be a positive integer")
	}
	if len(e.Attributes) > 0 {
		if len(e.Attributes) > MaxAttributes {
			return fmt.Errorf("attributes exceed %d bytes", MaxAttributes)
		}
		if a := bytes.TrimSpace(e.Attributes); len(a) == 0 || a[0] != '{' || !json.Valid(a) {
			return errors.New("attributes must be a JSON object")
		}
	}
	return nil
}

func ValidateBatch(events []Event, now time.Time) error {
	if len(events) == 0 {
		return errors.New("no events")
	}
	if len(events) > MaxBatch {
		return fmt.Errorf("more than %d events", MaxBatch)
	}
	for i, e := range events {
		if err := e.Validate(now); err != nil {
			return fmt.Errorf("event %d: %w", i, err)
		}
	}
	return nil
}

type Producer interface {
	Produce(ctx context.Context, events []Event) error
	Ping(ctx context.Context) error
}

// ErrBadData marks an error that will repeat on every retry of the same batch.
var ErrBadData = errors.New("the database rejected the events as bad data")

// EventWriter stores usage events once (ADR-0007).
type EventWriter interface {
	// Write returns only the new events; an ErrBadData error means a retry cannot succeed.
	Write(ctx context.Context, events []Event) ([]Event, error)
}
