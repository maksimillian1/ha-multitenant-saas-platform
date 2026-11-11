// Package events publishes every tenant's state to the compacted Kafka topic
// tenant-states, keyed by tenant_id (ADR-0012, item 5). The API Gateway reads
// it and rejects a tenant that is suspended or deleted. Publishing is best
// effort: the admin's request never fails on it, and Resync, run every
// minute, repairs a publish that was lost.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"

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

// Publish sends t's state and logs a failure instead of returning it.
func (p *Publisher) Publish(ctx context.Context, t core.Tenant) {
	if err := p.send(ctx, []core.Tenant{t}); err != nil {
		p.log.Error("publishing the tenant state failed; the next resync repairs it", "tenant_id", t.ID, "state", t.State, "err", err)
	}
}

// Resync publishes the state of every tenant, deleted ones included.
func (p *Publisher) Resync(ctx context.Context, list func(context.Context) ([]core.Tenant, error)) error {
	tenants, err := list(ctx)
	if err != nil {
		return err
	}
	return p.send(ctx, tenants)
}

// Run resyncs at start and then every interval until ctx ends.
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
		// franz-go keeps a record that waits for metadata past ctx. A new
		// client drops it, so a long outage neither blocks nor fills a buffer;
		// the resync sends the states again.
		fresh, err := kgo.NewClient(p.opts...)
		if err != nil {
			return err
		}
		p.client = fresh
		go client.Close()
		return ctx.Err()
	}
}

// KafkaSCRAM is the SCRAM-SHA-512 login of this binary's KafkaUser (HAMSP-31).
// No username means no authentication, for tests against kfake.
func KafkaSCRAM(username, password string) ([]kgo.Opt, error) {
	if username == "" {
		return nil, nil
	}
	if password == "" {
		return nil, errors.New("KAFKA_PASSWORD is required when KAFKA_USERNAME is set")
	}
	auth := scram.Auth{User: username, Pass: password}
	return []kgo.Opt{kgo.SASL(auth.AsSha512Mechanism())}, nil
}
