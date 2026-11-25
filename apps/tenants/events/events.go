// Package events publishes best effort: requests never fail on it, and the per-minute Resync
// repairs lost publishes (ADR-0012, item 5).
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/tenants/core"
)

const publishTimeout = 5 * time.Second

type event struct {
	TenantID string     `json:"tenant_id"`
	State    core.State `json:"state"`
	At       time.Time  `json:"at"`
}

type Publisher struct {
	opts []kgo.Opt
	log  *slog.Logger

	mu     sync.Mutex
	client *kgo.Client
}

func NewPublisher(brokers []string, topic string, log *slog.Logger, extra ...kgo.Opt) (*Publisher, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(topic),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordDeliveryTimeout(publishTimeout),
	}
	opts = append(opts, extra...)
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return &Publisher{opts: opts, log: log, client: client}, nil
}

func (p *Publisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.client.Close()
}

func (p *Publisher) Publish(ctx context.Context, t core.Tenant) {
	if err := p.send(ctx, []core.Tenant{t}); err != nil {
		p.log.Error("publishing the tenant state failed; the next resync repairs it", "tenant_id", t.ID, "state", t.State, "err", err)
	}
}

// Deleted tenants are included.
func (p *Publisher) Resync(ctx context.Context, list func(context.Context) ([]core.Tenant, error)) error {
	tenants, err := list(ctx)
	if err != nil {
		return err
	}
	return p.send(ctx, tenants)
}

func (p *Publisher) Run(ctx context.Context, every time.Duration, list func(context.Context) ([]core.Tenant, error)) {
	for {
		if err := p.Resync(ctx, list); err != nil && ctx.Err() == nil {
			p.log.Error("tenant state resync failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (p *Publisher) send(ctx context.Context, tenants []core.Tenant) error {
	if len(tenants) == 0 {
		return nil
	}
	now := time.Now().UTC()
	records := make([]*kgo.Record, len(tenants))
	for i, t := range tenants {
		value, err := json.Marshal(event{TenantID: t.ID, State: t.State, At: now})
		if err != nil {
			return err
		}
		records[i] = &kgo.Record{Key: []byte(t.ID), Value: value}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	client := p.client
	done := make(chan error, 1)
	go func() { done <- client.ProduceSync(ctx, records...).FirstErr() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// franz-go keeps a record waiting for metadata past ctx; a new client drops it, so an outage
		// neither blocks nor fills a buffer.
		fresh, err := kgo.NewClient(p.opts...)
		if err != nil {
			return err
		}
		p.client = fresh
		go client.Close()
		return ctx.Err()
	}
}
