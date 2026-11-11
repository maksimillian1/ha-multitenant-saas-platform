// Package tenantstate follows the tenant states that Tenant Management
// publishes to the compacted Kafka topic tenant-states (ADR-0012, item 5) and
// answers whether a tenant is blocked. The state is kept in memory, so the
// request path makes no call; while Kafka is down the last state read stays.
package tenantstate

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Event is one record of tenant-states, keyed by tenant_id.
type Event struct {
	TenantID string `json:"tenant_id"`
	State    string `json:"state"`
}

var blocked = map[string]bool{"suspended": true, "deleting": true, "deleted": true}

type States struct {
	mu      sync.RWMutex
	blocked map[string]bool
}

func New() *States { return &States{blocked: map[string]bool{}} }

// Blocked reports whether the tenant's latest state is suspended, deleting or
// deleted. A tenant not seen yet is not blocked.
func (s *States) Blocked(tenant string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.blocked[tenant]
}

func (s *States) apply(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if blocked[e.State] {
		s.blocked[e.TenantID] = true
	} else {
		delete(s.blocked, e.TenantID)
	}
}

// Follow reads the whole topic from the start, without a consumer group, so
// every replica holds every tenant, and keeps reading until ctx ends.
func (s *States) Follow(ctx context.Context, brokers []string, topic string, log *slog.Logger) error {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		return err
	}
	defer client.Close()
	for ctx.Err() == nil {
		fetches := client.PollFetches(ctx)
		fetches.EachError(func(t string, p int32, err error) {
			if ctx.Err() == nil {
				log.Warn("tenant-states fetch failed, keeping the last state", "partition", p, "err", err)
			}
		})
		fetches.EachRecord(func(r *kgo.Record) {
			var e Event
			if err := json.Unmarshal(r.Value, &e); err != nil || e.TenantID != string(r.Key) {
				log.Error("skipping a bad tenant-states record", "offset", r.Offset, "err", err)
				return
			}
			s.apply(e)
		})
	}
	return ctx.Err()
}
