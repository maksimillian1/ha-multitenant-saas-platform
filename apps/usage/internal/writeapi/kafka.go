package writeapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/event"
)

// Kafka produces every event to the topic with tenant_id as the key, so one
// tenant's events keep their order (ADR-0001 item 4). Writes wait for all
// in-sync replicas and the producer is idempotent, so a retried send is not
// written twice (ADR-0005).
type Kafka struct {
	client *kgo.Client
}

var _ Producer = (*Kafka)(nil)

func NewKafka(brokers []string, topic string, extra ...kgo.Opt) (*Kafka, error) {
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
	return &Kafka{client: client}, nil
}

func (k *Kafka) Close() { k.client.Close() }

func (k *Kafka) Ping(ctx context.Context) error { return k.client.Ping(ctx) }

func (k *Kafka) Produce(ctx context.Context, events []event.Event) error {
	records := make([]*kgo.Record, len(events))
	for i, e := range events {
		value, err := json.Marshal(e)
		if err != nil {
			return err
		}
		records[i] = &kgo.Record{Key: []byte(e.TenantID), Value: value}
	}
	return k.client.ProduceSync(ctx, records...).FirstErr()
}
