// Package kafka connects the usage pipeline to the usage topic: UsageWriteAPI
// produces to it and Usage Writer consumes from it.
package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/maksimillian1/ha-multitenant-saas-platform/apps/usage/internal/core"
)

// Producer produces every event to the topic with tenant_id as the key, so one
// tenant's events keep their order. Writes wait for all
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
		value, err := Encode(e)
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

// SCRAM authenticates the client as its own KafkaUser with SCRAM-SHA-512
// (HAMSP-31): the brokers' ACLs then decide what that identity may do. No
// username means no authentication, for local tests against kfake; a username
// without a password is a configuration error.
func SCRAM(username, password string) ([]kgo.Opt, error) {
	if username == "" {
		return nil, nil
	}
	if password == "" {
		return nil, errors.New("KAFKA_PASSWORD is required when KAFKA_USERNAME is set")
	}
	auth := scram.Auth{User: username, Pass: password}
	return []kgo.Opt{kgo.SASL(auth.AsSha512Mechanism())}, nil
}

// Encode is the value of e's Kafka record in the current schema version.
func Encode(e core.Event) ([]byte, error) {
	return json.Marshal(core.Record{SchemaVersion: core.SchemaVersion, Event: e})
}

// Decode reads a record value of any schema version up to core.SchemaVersion.
// UsageWriteAPI validated the event before it reached Kafka; Decode checks
// only what the store needs to deduplicate it.
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
