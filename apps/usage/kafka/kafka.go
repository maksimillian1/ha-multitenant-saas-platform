// Package kafka connects the usage pipeline to the usage topic: UsageWriteAPI
// produces to it and Usage Writer consumes from it (ADR-0001 item 4).
package kafka

import (
	"context"
	"encoding/json"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/core"
)

// Producer produces every event to the topic with tenant_id as the key, so one
// tenant's events keep their order (ADR-0001 item 4). Writes wait for all
// in-sync replicas and the producer is idempotent, so a retried send is not
// written twice (ADR-0005).
type Producer struct {
	client *kgo.Client
}

var _ core.Producer = (*Producer)(nil)

func NewProducer(brokers []string, topic string, extra ...kgo.Opt) (*Producer, error) {
	opts := append([]kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(topic),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		// A request that Kafka cannot confirm fails within this time, so the
		// sender gets 503 and retries instead of hanging.
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
		value, err := json.Marshal(e)
		if err != nil {
			return err
		}
		records[i] = &kgo.Record{Key: []byte(e.TenantID), Value: value}
	}
	return p.client.ProduceSync(ctx, records...).FirstErr()
}

// NewConsumer is the consumer group client: offsets are committed only by
// Usage Writer, and a new group starts at the beginning of the topic, so
// events produced before the first writer started are not skipped.
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
