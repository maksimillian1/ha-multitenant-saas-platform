package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/core"
)

// tenant_id is the key, so one tenant's events keep their order (ADR-0005).
type Producer struct {
	client *kgo.Client
}

var _ core.Producer = (*Producer)(nil)

func NewProducer(brokers []string, topic string, extra ...kgo.Opt) (*Producer, error) {
	opts := append([]kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(topic),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		// The sender gets 503 and retries instead of hanging.
		kgo.RecordDeliveryTimeout(10 * time.Second),
	}, extra...)
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	return &Producer{client: client}, nil
}

func (p *Producer) Close() { p.client.Close() }

func (p *Producer) Ping(ctx context.Context) error { return p.client.Ping(ctx) }

func (p *Producer) Produce(ctx context.Context, events []core.Event) error {
	records := make([]*kgo.Record, len(events))
	for i, e := range events {
		value, err := Encode(e)
		if err != nil {
			return err
		}
		records[i] = &kgo.Record{Key: []byte(e.TenantID), Value: value}
	}
	return p.client.ProduceSync(ctx, records...).FirstErr()
}

// A new group starts at the beginning, so events produced before the first writer are not skipped.
func NewConsumer(brokers []string, topic, group string, extra ...kgo.Opt) (*kgo.Client, error) {
	opts := append([]kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topic),
		kgo.DisableAutoCommit(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	}, extra...)
	return kgo.NewClient(opts...)
}

func Encode(e core.Event) ([]byte, error) {
	return json.Marshal(core.Record{SchemaVersion: core.SchemaVersion, Event: e})
}

// Decode checks only what the store needs to deduplicate; UsageWriteAPI validated the rest.
func Decode(value []byte) (core.Event, error) {
	var r core.Record
	if err := json.Unmarshal(value, &r); err != nil {
		return core.Event{}, err
	}
	if r.SchemaVersion < 0 || r.SchemaVersion > core.SchemaVersion {
		return core.Event{}, fmt.Errorf("schema_version %d is not supported, want 1 to %d", r.SchemaVersion, core.SchemaVersion)
	}
	if r.ID == "" || r.TenantID == "" || r.Time.IsZero() {
		return core.Event{}, errors.New("event_id, tenant_id and event_time are required")
	}
	return r.Event, nil
}
