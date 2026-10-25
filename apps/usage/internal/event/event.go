// Package event is a usage event as UsageWriteAPI accepts it and as it travels
// through Kafka (docs/contracts/usage-events.md).
package event

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Metrics a sender may report. Agent tokens (HAMSP-18) and egress (HAMSP-19)
// are added with their senders.
var Metrics = map[string]bool{
	"api_calls": true,
}

const (
	MaxBatch         = 1000
	MaxAttributes    = 4 << 10
	MaxAge           = 7 * 24 * time.Hour
	MaxClockSkew     = 5 * time.Minute
	attributesObject = '{'
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
		if a := bytes.TrimSpace(e.Attributes); len(a) == 0 || a[0] != attributesObject || !json.Valid(a) {
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
