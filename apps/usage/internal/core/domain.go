// Package core holds the usage event as UsageWriteAPI accepts it and as it
// travels through Kafka (docs/contracts/usage-event), the interfaces
// between the pipeline's parts, and the configuration. It is linked into every
// binary, so it imports nothing but the standard library and uuid
// (.golangci.yml).
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

// Metrics a sender may report. Agent tokens and egress are added with their
// senders.
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

// SchemaVersion is the version of the Kafka record
// (docs/contracts/usage-event/schemas/kafka-record.vN.json). UsageWriteAPI
// sets it; Usage Writer reads every version it declares in participants.json,
// so a new version is added to writer before writeapi writes it (ADR-0010).
// The HTTP contract is versioned by its path, /v1/events, on its own.
const SchemaVersion = 1

// Record is the value of one Kafka record. A record without schema_version was
// produced before versions existed and is version 1.
type Record struct {
	SchemaVersion int `json:"schema_version"`
	Event
}

// Validate checks one event against the contract at time now.
func (e Event) Validate(now time.Time) error {
	switch {
	case e.ID == "":
		return errors.New("event_id is required")
	case uuid.Validate(e.ID) != nil:
		return errors.New("event_id must be a UUID")
	case e.Time.IsZero():
		return errors.New("event_time is required")
	case e.Time.Before(now.Add(-MaxAge)) || e.Time.After(now.Add(MaxClockSkew)):
		return fmt.Errorf("event_time %s is outside [now - %s, now + %s]", e.Time.Format(time.RFC3339), MaxAge, MaxClockSkew)
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

// ValidateBatch rejects the whole batch if any event is invalid.
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

// Producer writes events to Kafka and returns only once all of them are
// confirmed, or with an error.
type Producer interface {
	Produce(ctx context.Context, events []Event) error
	Ping(ctx context.Context) error
}

// EventWriter stores usage events once (ADR-0007).
type EventWriter interface {
	// Write stores a batch in one transaction and returns the events that were
	// new. Events stored before, in this batch or an earlier one, are skipped.
	Write(ctx context.Context, events []Event) ([]Event, error)
}
