// Package tenantstate keeps states in memory, so the request path makes no call; while Kafka is
// down the last state read stays (ADR-0012).
package tenantstate

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"
)

type Event struct {
	TenantID string `json:"tenant_id"`
	State    string `json:"state"`
}

// A state not listed is ignored, so the tenant keeps its earlier state.
var blocked = map[string]bool{
	"provisioning": false, "active": false,
	"suspended": true, "deleting": true, "deleted": true,
}

type States struct {
	mu      sync.RWMutex
	blocked map[string]bool
}

func New() *States { return &States{blocked: map[string]bool{}} }

// A tenant not seen yet is not blocked.
func (s *States) Blocked(tenant string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.blocked[tenant]
}

func (s *States) apply(e Event) {
	block, known := blocked[e.State]
	if !known {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if block {
		s.blocked[e.TenantID] = true
	} else {
		delete(s.blocked, e.TenantID)
	}
}

// No consumer group, so every replica holds every tenant.
func (s *States) Follow(ctx context.Context, brokers []string, topic string, log *slog.Logger, extra ...kgo.Opt) error {
	client, err := kgo.NewClient(append([]kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	}, extra...)...)
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
